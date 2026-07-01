package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Config controls one repository analysis without mutating the target tree.
type Config struct {
	Root      string
	BuildTags []string
}

type functionMeta struct {
	ssaFunction  *ssa.Function
	function     Function
	directEdge   bool
	localCalls   []string
	externalCall bool
}

// Analyze loads a Go working tree and returns its typed local call graph.
func Analyze(ctx context.Context, config Config) (*Index, error) {
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve analysis root: %w", err)
	}
	buildFlags := make([]string, 0, 1)
	if len(config.BuildTags) > 0 {
		buildFlags = append(buildFlags, "-tags="+strings.Join(config.BuildTags, ","))
	}
	packageConfig := &packages.Config{
		Context:    ctx,
		Dir:        root,
		Tests:      true,
		BuildFlags: buildFlags,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedImports | packages.NeedDeps,
	}
	loaded, loadErr := packages.Load(packageConfig, "./...")
	if loadErr != nil {
		report := LoadReport{Root: root, BuildTags: append([]string(nil), config.BuildTags...)}
		return nil, fmt.Errorf("load Go packages: %w\nReproduce with: %s", loadErr, report.reproductionCommand())
	}
	if len(loaded) == 0 {
		report := LoadReport{Root: root, BuildTags: append([]string(nil), config.BuildTags...)}
		return nil, fmt.Errorf("load Go packages: no packages found beneath %s\nReproduce with: %s", root, report.reproductionCommand())
	}
	loadReport := collectLoadReport(root, config.BuildTags, loaded)
	healthyPackages := make([]*packages.Package, 0, len(loaded))
	for _, loadedPackage := range loaded {
		if len(loadedPackage.Errors) == 0 {
			healthyPackages = append(healthyPackages, loadedPackage)
		}
	}
	if len(healthyPackages) == 0 {
		return nil, fmt.Errorf("load Go packages: no analyzable packages beneath %s\n%s", root, loadReport.String())
	}

	program, ssaPackages := ssautil.AllPackages(healthyPackages, ssa.InstantiateGenerics)
	program.Build()
	packageByTypes := packagesByTypes(healthyPackages)
	metas := collectFunctions(root, program, ssaPackages, packageByTypes)
	if len(metas) == 0 {
		return nil, fmt.Errorf("analyze Go packages: no local named functions found beneath %s", root)
	}

	initialGraph := cha.CallGraph(program)
	callGraph := vta.CallGraph(ssautil.AllFunctions(program), initialGraph)
	edges := collectEdges(root, callGraph.Nodes, metas)
	classifyFunctions(metas, edges)

	index := &Index{
		Root: root, Functions: make(map[string]Function, len(metas)), Edges: edges,
		Outgoing: make(map[string][]Edge), Incoming: make(map[string][]Edge), LoadReport: loadReport,
	}
	for id, meta := range metas {
		index.Functions[id] = meta.function
	}
	for _, edge := range edges {
		index.Outgoing[edge.CallerID] = append(index.Outgoing[edge.CallerID], edge)
		index.Incoming[edge.CalleeID] = append(index.Incoming[edge.CalleeID], edge)
	}
	return index, nil
}

// packagesByTypes indexes all loaded package variants, including tests.
func packagesByTypes(roots []*packages.Package) map[*types.Package]*packages.Package {
	result := make(map[*types.Package]*packages.Package)
	packages.Visit(roots, nil, func(pkg *packages.Package) {
		if pkg.Types != nil && len(pkg.Syntax) > 0 {
			result[pkg.Types] = pkg
		}
	})
	return result
}

// collectFunctions converts named local SSA functions into stable browser records.
func collectFunctions(root string, program *ssa.Program, ssaPackages []*ssa.Package, packageByTypes map[*types.Package]*packages.Package) map[string]*functionMeta {
	// AllFunctions includes dependencies; ssaPackages contains only the roots selected by ./....
	localPackages := make(map[*ssa.Package]bool, len(ssaPackages))
	for _, ssaPackage := range ssaPackages {
		if ssaPackage != nil {
			localPackages[ssaPackage] = true
		}
	}
	result := make(map[string]*functionMeta)
	for ssaFunction := range ssautil.AllFunctions(program) {
		declaration, isDeclaration := ssaFunction.Syntax().(*ast.FuncDecl)
		if !isDeclaration || declaration.Name == nil || ssaFunction.Pkg == nil || !localPackages[ssaFunction.Pkg] {
			continue
		}
		position := program.Fset.PositionFor(declaration.Pos(), true)
		endPosition := program.Fset.PositionFor(declaration.End(), true)
		if !isLocalFile(root, position.Filename) {
			continue
		}
		loadedPackage := packageByTypes[ssaFunction.Pkg.Pkg]
		if loadedPackage == nil {
			continue
		}
		isTestFunction := strings.HasSuffix(position.Filename, "_test.go")
		isAugmentedProductionCopy := !isTestFunction && loadedPackage.ID != loadedPackage.PkgPath
		if isAugmentedProductionCopy {
			continue
		}
		signature := ssaFunction.Signature
		qualifiedName := functionName(ssaFunction, signature)
		id := stableFunctionID(ssaFunction)
		fullDocumentation := ""
		if declaration.Doc != nil {
			fullDocumentation = declaration.Doc.Text()
		}
		documentation := firstParagraph(fullDocumentation)
		source := readSource(position, endPosition)
		classification, directEdge, externalCall := classifyDirect(declaration, loadedPackage.TypesInfo, fullDocumentation)
		result[id] = &functionMeta{
			ssaFunction:  ssaFunction,
			directEdge:   directEdge,
			externalCall: externalCall,
			function: Function{
				ID: id, Name: ssaFunction.Name(), QualifiedName: qualifiedName,
				Package: ssaFunction.Pkg.Pkg.Path(), Signature: readableType(signature),
				Parameters: tupleStrings(signature.Params(), signature.Variadic()),
				Results:    tupleStrings(signature.Results(), false), Contracts: signatureContracts(signature),
				Intent: documentation, IntentSource: intentSource(documentation),
				File: position.Filename, Line: position.Line, EndLine: endPosition.Line,
				Source: source, Test: isTestFunction,
				Classification: classification,
			},
		}
	}
	return result
}

// collectEdges retains calls whose endpoints are both named local functions.
func collectEdges(root string, nodes map[*ssa.Function]*callgraph.Node, metas map[string]*functionMeta) []Edge {
	_ = root
	idByFunction := make(map[*ssa.Function]string, len(metas))
	for id, meta := range metas {
		idByFunction[meta.ssaFunction] = id
	}
	edgeKeys := make(map[string]bool)
	edges := make([]Edge, 0)
	addEdge := func(callerID string, calleeID string, dynamic bool, callSite string) {
		key := fmt.Sprintf("%s|%s|%t|%s", callerID, calleeID, dynamic, callSite)
		if edgeKeys[key] {
			return
		}
		edgeKeys[key] = true
		edges = append(edges, Edge{CallerID: callerID, CalleeID: calleeID, Dynamic: dynamic, CallSite: callSite})
	}
	for callerID, meta := range metas {
		for _, block := range meta.ssaFunction.Blocks {
			for _, instruction := range block.Instrs {
				call, isCall := instruction.(ssa.CallInstruction)
				if !isCall || call.Common().IsInvoke() {
					continue
				}
				callee := call.Common().StaticCallee()
				calleeID, local := idByFunction[callee]
				if !local {
					calleeID = stableFunctionID(callee)
					_, local = metas[calleeID]
				}
				if !local {
					if callee != nil && callee.Pkg != nil && knownPurePackages[callee.Pkg.Pkg.Path()] {
						continue
					}
					if callee != nil {
						meta.externalCall = true
					}
					continue
				}
				position := meta.ssaFunction.Prog.Fset.PositionFor(call.Pos(), true)
				addEdge(callerID, calleeID, false, fmt.Sprintf("%s:%d", position.Filename, position.Line))
			}
		}
	}
	for ssaFunction, node := range nodes {
		callerID, callerExists := idByFunction[ssaFunction]
		if !callerExists {
			continue
		}
		for _, graphEdge := range node.Out {
			if graphEdge.Site == nil || !graphEdge.Site.Common().IsInvoke() {
				continue
			}
			calleeID, calleeExists := idByFunction[graphEdge.Callee.Func]
			if !calleeExists {
				calleeID = stableFunctionID(graphEdge.Callee.Func)
				_, calleeExists = metas[calleeID]
			}
			if !calleeExists {
				metas[callerID].externalCall = true
				continue
			}
			dynamic := graphEdge.Site != nil && graphEdge.Site.Common().IsInvoke()
			callSite := ""
			if graphEdge.Site != nil {
				position := graphEdge.Caller.Func.Prog.Fset.PositionFor(graphEdge.Site.Pos(), true)
				callSite = fmt.Sprintf("%s:%d", position.Filename, position.Line)
			}
			addEdge(callerID, calleeID, dynamic, callSite)
		}
	}
	sort.Slice(edges, func(left, right int) bool {
		if edges[left].CallerID == edges[right].CallerID {
			return edges[left].CalleeID < edges[right].CalleeID
		}
		return edges[left].CallerID < edges[right].CallerID
	})
	return edges
}

// stableFunctionID collapses test-augmented copies onto one source-backed symbol.
func stableFunctionID(function *ssa.Function) string {
	if function == nil || function.Pkg == nil {
		return ""
	}
	declaration, ok := function.Syntax().(*ast.FuncDecl)
	if !ok {
		return ""
	}
	position := function.Prog.Fset.PositionFor(declaration.Pos(), true)
	identity := fmt.Sprintf("%s|%s|%s:%d", function.Pkg.Pkg.Path(), functionName(function, function.Signature), position.Filename, position.Line)
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:16])
}

// functionName creates a compact qualified name suitable for symbol search.
func functionName(function *ssa.Function, signature *types.Signature) string {
	packageName := function.Pkg.Pkg.Name()
	if signature.Recv() == nil {
		return packageName + "." + function.Name()
	}
	return packageName + ".(" + readableType(signature.Recv().Type()) + ")." + function.Name()
}

// firstParagraph keeps authored intent concise in graph nodes.
func firstParagraph(documentation string) string {
	documentation = strings.TrimSpace(documentation)
	if split := strings.Index(documentation, "\n\n"); split >= 0 {
		return strings.TrimSpace(documentation[:split])
	}
	return documentation
}

// intentSource distinguishes source-authored text from generated summaries.
func intentSource(intent string) string {
	if intent == "" {
		return ""
	}
	return "documentation"
}

// isLocalFile reports whether filename is contained by the analyzed root.
func isLocalFile(root string, filename string) bool {
	relative, err := filepath.Rel(root, filename)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// readSource returns the exact function source when token offsets are available.
func readSource(start token.Position, end token.Position) string {
	contents, err := os.ReadFile(start.Filename)
	if err != nil || start.Offset < 0 || end.Offset > len(contents) || start.Offset >= end.Offset {
		return ""
	}
	return string(contents[start.Offset:end.Offset])
}
