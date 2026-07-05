// Package gobackend implements the built-in semantic analysis backend for Go repositories.
package gobackend

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

	"github.com/gtindo/flowmap/internal/semantic"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Backend converts Go compiler facts into a language-neutral semantic snapshot.
type Backend struct{}

type symbolMeta struct {
	ssaFunction *ssa.Function
	syntax      ast.Node
	typeInfo    *types.Info
	symbol      semantic.Symbol
}

// Analyze loads and analyzes one Go working tree.
func (Backend) Analyze(ctx context.Context, request semantic.AnalysisRequest) (semantic.Snapshot, error) {
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return semantic.Snapshot{}, fmt.Errorf("resolve analysis root: %w", err)
	}

	if err := checkActiveToolchain(ctx, root); err != nil {
		return semantic.Snapshot{}, err
	}

	buildFlags := make([]string, 0, 1)
	if len(request.BuildTags) > 0 {
		buildFlags = append(buildFlags, "-tags="+strings.Join(request.BuildTags, ","))
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
		return semantic.Snapshot{}, fmt.Errorf("load Go packages: %w\nReproduce with: %s", loadErr, reproductionCommand(root, request.BuildTags))
	}
	if len(loaded) == 0 {
		return semantic.Snapshot{}, fmt.Errorf("load Go packages: no packages found beneath %s\nReproduce with: %s", root, reproductionCommand(root, request.BuildTags))
	}

	diagnostics := collectDiagnosticReport(root, request.BuildTags, loaded)
	healthyPackages := make([]*packages.Package, 0, len(loaded))
	for _, loadedPackage := range loaded {
		if len(loadedPackage.Errors) == 0 {
			healthyPackages = append(healthyPackages, loadedPackage)
		}
	}
	if len(healthyPackages) == 0 {
		return semantic.Snapshot{}, fmt.Errorf("load Go packages: no analyzable packages beneath %s\n%s", root, formatDiagnosticReport(root, diagnostics))
	}

	program, ssaPackages := ssautil.AllPackages(healthyPackages, ssa.InstantiateGenerics)
	program.Build()

	metas := collectSymbols(root, program, ssaPackages, packagesByTypes(healthyPackages))
	if len(metas) == 0 {
		return semantic.Snapshot{}, fmt.Errorf("analyze Go packages: no local functions found beneath %s", root)
	}

	initialGraph := cha.CallGraph(program)
	callGraph := vta.CallGraph(ssautil.AllFunctions(program), initialGraph)
	relationships := collectRelationships(callGraph.Nodes, metas)

	ids := make([]string, 0, len(metas))
	for id := range metas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	symbols := make([]semantic.Symbol, 0, len(ids))
	for _, id := range ids {
		symbols = append(symbols, metas[id].symbol)
	}

	return semantic.Snapshot{Root: root, Symbols: symbols, Relationships: relationships, Diagnostics: diagnostics}, nil
}

func packagesByTypes(roots []*packages.Package) map[*types.Package]*packages.Package {
	result := make(map[*types.Package]*packages.Package)
	packages.Visit(roots, nil, func(pkg *packages.Package) {
		if pkg.Types != nil && len(pkg.Syntax) > 0 {
			result[pkg.Types] = pkg
		}
	})
	return result
}

func collectSymbols(root string, program *ssa.Program, ssaPackages []*ssa.Package, packageByTypes map[*types.Package]*packages.Package) map[string]*symbolMeta {
	localPackages := make(map[*ssa.Package]bool, len(ssaPackages))
	for _, ssaPackage := range ssaPackages {
		if ssaPackage != nil {
			localPackages[ssaPackage] = true
		}
	}

	result := make(map[string]*symbolMeta)
	for ssaFunction := range ssautil.AllFunctions(program) {
		syntax := ssaFunction.Syntax()
		declaration, isDeclaration := syntax.(*ast.FuncDecl)
		_, isLiteral := syntax.(*ast.FuncLit)
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
		if !isTestFunction && loadedPackage.ID != loadedPackage.PkgPath {
			continue
		}

		signature := ssaFunction.Signature
		qualifiedName := functionName(ssaFunction, signature)
		kind := semantic.SymbolFunction
		if signature.Recv() != nil {
			kind = semantic.SymbolMethod
		}
		if isLiteral {
			qualifiedName = ssaFunction.String()
			kind = semantic.SymbolClosure
		}
		documentation := ""
		if isDeclaration && declaration.Doc != nil {
			documentation = declaration.Doc.Text()
		}

		id := stableFunctionID(ssaFunction)
		result[id] = &symbolMeta{
			ssaFunction: ssaFunction,
			syntax:      syntax,
			typeInfo:    loadedPackage.TypesInfo,
			symbol: semantic.Symbol{
				ID: id, Kind: kind, Name: ssaFunction.Name(), QualifiedName: qualifiedName,
				Package:  ssaFunction.Pkg.Pkg.Path(),
				Location: semantic.Location{File: position.Filename, Line: position.Line, EndLine: endPosition.Line},
				Source:   readSource(position, endPosition), Documentation: documentation,
				Signature: semantic.Signature{
					Display: readableType(signature), Parameters: tupleStrings(signature.Params(), signature.Variadic()),
					Results: tupleStrings(signature.Results(), false), Contracts: signatureContracts(signature),
				},
				Test: isTestFunction, Facts: collectFacts(functionBody(syntax), loadedPackage.TypesInfo),
			},
		}
	}
	return result
}

func collectRelationships(nodes map[*ssa.Function]*callgraph.Node, metas map[string]*symbolMeta) []semantic.Relationship {
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

	keys := make(map[string]bool)
	relationships := make([]semantic.Relationship, 0)
	add := func(fromID, toID, kind, location, provenance, precision string, dynamic bool) {
		key := fmt.Sprintf("%s|%s|%s|%t|%s", fromID, toID, kind, dynamic, location)
		if keys[key] {
			return
		}
		keys[key] = true
		relationships = append(relationships, semantic.Relationship{
			FromID: fromID, ToID: toID, Kind: kind, Location: location,
			Provenance: provenance, Precision: precision, Dynamic: dynamic,
		})
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
					if callee != nil {
						packagePath := ""
						if callee.Pkg != nil {
							packagePath = callee.Pkg.Pkg.Path()
						}
						addExternalCallFact(meta, packagePath, callee.Name())
					}
					continue
				}
				position := meta.ssaFunction.Prog.Fset.PositionFor(call.Pos(), true)
				add(callerID, calleeID, semantic.RelationshipCall, formatPosition(position), "ssa", "exact", false)
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
				addExternalCallFact(metas[callerID], "", "")
				continue
			}
			position := graphEdge.Caller.Func.Prog.Fset.PositionFor(graphEdge.Site.Pos(), true)
			add(callerID, calleeID, semantic.RelationshipCall, formatPosition(position), "vta", "possible", true)
		}
	}

	collectDependencyRelationships(metas, idByObject, idBySyntax, add)
	sort.Slice(relationships, func(left, right int) bool {
		leftRelationship, rightRelationship := relationships[left], relationships[right]
		if leftRelationship.FromID != rightRelationship.FromID {
			return leftRelationship.FromID < rightRelationship.FromID
		}
		if leftRelationship.ToID != rightRelationship.ToID {
			return leftRelationship.ToID < rightRelationship.ToID
		}
		if leftRelationship.Kind != rightRelationship.Kind {
			return leftRelationship.Kind < rightRelationship.Kind
		}
		return leftRelationship.Location < rightRelationship.Location
	})
	return relationships
}

func collectDependencyRelationships(
	metas map[string]*symbolMeta,
	idByObject map[*types.Func]string,
	idBySyntax map[ast.Node]string,
	add func(string, string, string, string, string, string, bool),
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
				add(callerID, calleeID, semantic.RelationshipDependency, formatPosition(position), "syntax", "exact", false)
			}
			return true
		})
	}
}

func addExternalCallFact(meta *symbolMeta, packagePath, name string) {
	if meta == nil {
		return
	}
	for _, fact := range meta.symbol.Facts {
		if fact.Kind == semantic.FactExternalCall && fact.Package == packagePath && fact.Name == name {
			return
		}
	}
	meta.symbol.Facts = append(meta.symbol.Facts, semantic.Fact{Kind: semantic.FactExternalCall, Package: packagePath, Name: name})
}

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

func isFunctionExpression(expression ast.Expr, typeInfo *types.Info) bool {
	valueType := typeInfo.TypeOf(expression)
	if valueType == nil {
		return false
	}
	_, ok := valueType.Underlying().(*types.Signature)
	return ok
}

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

func functionName(function *ssa.Function, signature *types.Signature) string {
	packageName := function.Pkg.Pkg.Name()
	if signature.Recv() == nil {
		return packageName + "." + function.Name()
	}
	return packageName + ".(" + readableType(signature.Recv().Type()) + ")." + function.Name()
}

func isLocalFile(root, filename string) bool {
	relative, err := filepath.Rel(root, filename)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readSource(start, end token.Position) string {
	contents, err := os.ReadFile(start.Filename)
	if err != nil || start.Offset < 0 || end.Offset > len(contents) || start.Offset >= end.Offset {
		return ""
	}
	return string(contents[start.Offset:end.Offset])
}

func formatPosition(position token.Position) string {
	return fmt.Sprintf("%s:%d", position.Filename, position.Line)
}
