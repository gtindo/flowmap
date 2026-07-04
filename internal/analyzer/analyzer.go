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

// functionMeta retains compiler objects needed while assembling the public function model.
type functionMeta struct {
	ssaFunction  *ssa.Function
	syntax       ast.Node
	typeInfo     *types.Info
	function     Function
	directEdge   bool
	localCalls   []string
	externalCall bool
}

const (
	edgeKindCall       = "call"
	edgeKindDependency = "dependency"
)

// Analyze loads a Go working tree and returns its typed local call graph.
func Analyze(ctx context.Context, config Config) (*Index, error) {
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve analysis root: %w", err)
	}

	if err := checkActiveToolchain(ctx, root); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("analyze Go packages: no local functions found beneath %s", root)
	}

	initialGraph := cha.CallGraph(program)
	callGraph := vta.CallGraph(ssautil.AllFunctions(program), initialGraph)
	edges := collectEdges(callGraph.Nodes, metas)
	classifyFunctions(metas, edges)

	index := &Index{
		Root:       root,
		Functions:  make(map[string]Function, len(metas)),
		Edges:      edges,
		Outgoing:   make(map[string][]Edge),
		Incoming:   make(map[string][]Edge),
		LoadReport: loadReport,
	}

	for id, meta := range metas {
		index.Functions[id] = meta.function
	}

	for _, edge := range edges {
		index.Outgoing[edge.CallerID] = append(index.Outgoing[edge.CallerID], edge)
		index.Incoming[edge.CalleeID] = append(index.Incoming[edge.CalleeID], edge)
	}

	index.Git = captureGitSnapshot(ctx, root, index.Functions)

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

// collectFunctions converts named and anonymous local SSA functions into stable browser records.
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
		syntax := ssaFunction.Syntax()
		declaration, isDeclaration := syntax.(*ast.FuncDecl)
		literal, isLiteral := syntax.(*ast.FuncLit)
		if (!isDeclaration && !isLiteral) || ssaFunction.Pkg == nil || !localPackages[ssaFunction.Pkg] {
			continue
		}
		if isDeclaration && declaration.Name == nil {
			continue
		}
		position := program.Fset.PositionFor(syntax.Pos(), true)
		endPosition := program.Fset.PositionFor(syntax.End(), true)
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
		if isLiteral {
			qualifiedName = ssaFunction.String()
		}
		id := stableFunctionID(ssaFunction)
		fullDocumentation := ""
		var body *ast.BlockStmt
		if isDeclaration {
			body = declaration.Body
		}
		if isLiteral {
			body = literal.Body
		}
		if isDeclaration && declaration.Doc != nil {
			fullDocumentation = declaration.Doc.Text()
		}
		documentation := firstParagraph(fullDocumentation)
		source := readSource(position, endPosition)
		classification, directEdge, externalCall := classifyDirect(body, loadedPackage.TypesInfo, fullDocumentation)
		result[id] = &functionMeta{
			ssaFunction:  ssaFunction,
			syntax:       syntax,
			typeInfo:     loadedPackage.TypesInfo,
			directEdge:   directEdge,
			externalCall: externalCall,
			function: Function{
				ID: id, Name: ssaFunction.Name(), QualifiedName: qualifiedName,
				Package: ssaFunction.Pkg.Pkg.Path(), Signature: readableType(signature),
				Parameters: tupleStrings(signature.Params(), signature.Variadic()),
				Results:    tupleStrings(signature.Results(), false), Contracts: signatureContracts(signature),
				Intent: documentation, IntentSource: intentSource(documentation),
				File: position.Filename, Line: position.Line, EndLine: endPosition.Line,
				Source: source, Test: isTestFunction, Anonymous: isLiteral,
				Classification: classification,
			},
		}
	}
	return result
}

// collectEdges retains local calls and statically identifiable function dependencies.
func collectEdges(nodes map[*ssa.Function]*callgraph.Node, metas map[string]*functionMeta) []Edge {
	idByFunction := make(map[*ssa.Function]string, len(metas))
	idByObject := make(map[*types.Func]string, len(metas))
	idBySyntax := make(map[ast.Node]string, len(metas))
	for id, meta := range metas {
		idByFunction[meta.ssaFunction] = id
		idBySyntax[meta.syntax] = id
		if object, ok := meta.ssaFunction.Object().(*types.Func); ok {
			idByObject[object] = id
		}
	}
	edgeKeys := make(map[string]bool)
	edges := make([]Edge, 0)
	addEdge := func(callerID string, calleeID string, kind string, dynamic bool, callSite string) {
		key := fmt.Sprintf("%s|%s|%s|%t|%s", callerID, calleeID, kind, dynamic, callSite)
		if edgeKeys[key] {
			return
		}
		edgeKeys[key] = true
		edges = append(edges, Edge{CallerID: callerID, CalleeID: calleeID, Kind: kind, Dynamic: dynamic, CallSite: callSite})
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
				addEdge(callerID, calleeID, edgeKindCall, false, fmt.Sprintf("%s:%d", position.Filename, position.Line))
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
			addEdge(callerID, calleeID, edgeKindCall, dynamic, callSite)
		}
	}
	collectDependencyEdges(metas, idByObject, idBySyntax, addEdge)
	sort.Slice(edges, func(left, right int) bool {
		if edges[left].CallerID == edges[right].CallerID {
			if edges[left].CalleeID == edges[right].CalleeID {
				if edges[left].Kind == edges[right].Kind {
					return edges[left].CallSite < edges[right].CallSite
				}
				return edges[left].Kind < edges[right].Kind
			}
			return edges[left].CalleeID < edges[right].CalleeID
		}
		return edges[left].CallerID < edges[right].CallerID
	})
	return edges
}

// collectDependencyEdges records local functions passed directly to calls.
func collectDependencyEdges(
	metas map[string]*functionMeta,
	idByObject map[*types.Func]string,
	idBySyntax map[ast.Node]string,
	addEdge func(string, string, string, bool, string),
) {
	for callerID, meta := range metas {
		body := functionBody(meta.syntax)
		if body == nil || meta.typeInfo == nil {
			continue
		}
		ast.Inspect(body, func(node ast.Node) bool {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, argument := range call.Args {
				if !isFunctionExpression(argument, meta.typeInfo) {
					continue
				}
				calleeID := referencedFunctionID(argument, meta.typeInfo, idByObject, idBySyntax)
				if calleeID == "" {
					continue
				}
				position := meta.ssaFunction.Prog.Fset.PositionFor(argument.Pos(), true)
				addEdge(callerID, calleeID, edgeKindDependency, false, fmt.Sprintf("%s:%d", position.Filename, position.Line))
			}
			return true
		})
	}
}

// functionBody extracts the executable body from named and anonymous functions.
func functionBody(syntax ast.Node) *ast.BlockStmt {
	switch typed := syntax.(type) {
	case *ast.FuncDecl:
		return typed.Body
	case *ast.FuncLit:
		return typed.Body
	default:
		return nil
	}
}

// isFunctionExpression reports whether an expression has a callable signature.
func isFunctionExpression(expression ast.Expr, typeInfo *types.Info) bool {
	valueType := typeInfo.TypeOf(expression)
	if valueType == nil {
		return false
	}
	_, ok := valueType.Underlying().(*types.Signature)
	return ok
}

// referencedFunctionID resolves named and anonymous function values to stable local IDs.
func referencedFunctionID(expression ast.Expr, typeInfo *types.Info, idByObject map[*types.Func]string, idBySyntax map[ast.Node]string) string {
	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return referencedFunctionID(typed.X, typeInfo, idByObject, idBySyntax)
	case *ast.Ident:
		function, _ := typeInfo.Uses[typed].(*types.Func)
		return idByObject[function]
	case *ast.SelectorExpr:
		if selection := typeInfo.Selections[typed]; selection != nil {
			function, _ := selection.Obj().(*types.Func)
			return idByObject[function]
		}
		function, _ := typeInfo.Uses[typed.Sel].(*types.Func)
		return idByObject[function]
	case *ast.FuncLit:
		return idBySyntax[typed]
	case *ast.IndexExpr:
		return referencedFunctionID(typed.X, typeInfo, idByObject, idBySyntax)
	case *ast.IndexListExpr:
		return referencedFunctionID(typed.X, typeInfo, idByObject, idBySyntax)
	case *ast.CallExpr:
		if typeAndValue, ok := typeInfo.Types[typed.Fun]; ok && typeAndValue.IsType() && len(typed.Args) == 1 {
			return referencedFunctionID(typed.Args[0], typeInfo, idByObject, idBySyntax)
		}
	}
	return ""
}

// stableFunctionID collapses test-augmented copies onto one source-backed symbol.
func stableFunctionID(function *ssa.Function) string {
	if function == nil || function.Pkg == nil {
		return ""
	}
	syntax := function.Syntax()
	if _, declaration := syntax.(*ast.FuncDecl); !declaration {
		if _, literal := syntax.(*ast.FuncLit); !literal {
			return ""
		}
	}
	if syntax == nil {
		return ""
	}
	position := function.Prog.Fset.PositionFor(syntax.Pos(), true)
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
