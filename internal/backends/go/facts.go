package gobackend

import (
	"go/ast"
	"go/types"
	"sort"

	"github.com/gtindo/flowmap/internal/semantic"
)

// collectFacts translates Go syntax and type evidence without classifying it.
func collectFacts(body *ast.BlockStmt, typeInfo *types.Info) []semantic.Fact {
	if body == nil {
		return []semantic.Fact{{Kind: semantic.FactDeclarationWithoutBody}}
	}

	factsByKey := make(map[semantic.Fact]bool)
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GoStmt:
			factsByKey[semantic.Fact{Kind: semantic.FactStartsConcurrentWork}] = true
		case *ast.SendStmt:
			factsByKey[semantic.Fact{Kind: semantic.FactChannelSend}] = true
		case *ast.AssignStmt:
			for _, left := range typed.Lhs {
				collectAssignmentFact(left, typeInfo, factsByKey)
			}
		case *ast.IncDecStmt:
			collectAssignmentFact(typed.X, typeInfo, factsByKey)
		case *ast.CallExpr:
			packagePath, callName := calledPackage(typed.Fun, typeInfo)
			if packagePath != "" {
				factsByKey[semantic.Fact{Kind: semantic.FactExternalCall, Package: packagePath, Name: callName}] = true
			}
		}
		return true
	})

	facts := make([]semantic.Fact, 0, len(factsByKey))
	for fact := range factsByKey {
		facts = append(facts, fact)
	}
	sort.Slice(facts, func(left, right int) bool {
		if facts[left].Kind != facts[right].Kind {
			return facts[left].Kind < facts[right].Kind
		}
		if facts[left].Package != facts[right].Package {
			return facts[left].Package < facts[right].Package
		}
		return facts[left].Name < facts[right].Name
	})
	return facts
}

func collectAssignmentFact(expression ast.Expr, typeInfo *types.Info, facts map[semantic.Fact]bool) {
	switch target := expression.(type) {
	case *ast.Ident:
		object, ok := typeInfo.Uses[target].(*types.Var)
		if ok && object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
			facts[semantic.Fact{Kind: semantic.FactWritesPackageState}] = true
		}
	case *ast.SelectorExpr:
		facts[semantic.Fact{Kind: semantic.FactWritesObjectState}] = true
	case *ast.IndexExpr:
		facts[semantic.Fact{Kind: semantic.FactWritesIndexedState}] = true
	}
}

func calledPackage(function ast.Expr, typeInfo *types.Info) (string, string) {
	selector, ok := function.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	packageName, ok := typeInfo.Uses[identifier].(*types.PkgName)
	if !ok {
		return "", ""
	}
	return packageName.Imported().Path(), selector.Sel.Name
}
