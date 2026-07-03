package analyzer

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
)

const (
	classificationPure    = "pure"
	classificationEdge    = "edge"
	classificationUnknown = "unknown"
	provenanceAuthored    = "authored"
	provenanceInferred    = "inferred"
)

var edgePackagePrefixes = []string{
	"database/sql", "net", "os", "log", "runtime", "time", "math/rand", "crypto/rand", "sync",
}

var knownPurePackages = map[string]bool{
	"bytes": true, "encoding/hex": true, "encoding/json": true, "fmt": true,
	"math": true, "path": true, "path/filepath": true, "strconv": true, "strings": true,
}

// classifyDirect records authored labels and locally visible effect evidence.
func classifyDirect(body *ast.BlockStmt, typeInfo *types.Info, documentation string) (Classification, bool, bool) {
	normalizedDocumentation := strings.ToLower(documentation)
	if strings.Contains(normalizedDocumentation, "operations (pure)") {
		return Classification{Kind: classificationPure, Provenance: provenanceAuthored, Evidence: []string{"function documentation declares Operations (Pure)"}}, false, false
	}
	if strings.Contains(normalizedDocumentation, "side effect (edge)") || strings.Contains(normalizedDocumentation, "side effects (edge)") {
		return Classification{Kind: classificationEdge, Provenance: provenanceAuthored, Evidence: []string{"function documentation declares Side Effect (Edge)"}}, true, false
	}
	// Assembly- and linker-backed declarations have effects that Go syntax cannot reveal.
	if body == nil {
		return Classification{Kind: classificationUnknown, Provenance: provenanceInferred}, false, true
	}

	evidence := make(map[string]bool)
	externalCall := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncLit:
			// A nested function owns the effects in its body.
			return false
		case *ast.GoStmt:
			evidence["starts a goroutine"] = true
		case *ast.SendStmt:
			evidence["sends on a channel"] = true
		case *ast.AssignStmt:
			for _, left := range typed.Lhs {
				inspectAssignment(left, typeInfo, evidence)
			}
		case *ast.IncDecStmt:
			inspectAssignment(typed.X, typeInfo, evidence)
		case *ast.CallExpr:
			packagePath, callName := calledPackage(typed.Fun, typeInfo)
			if packagePath == "" {
				return true
			}
			if isEdgePackage(packagePath) {
				evidence[fmt.Sprintf("calls %s.%s", packagePath, callName)] = true
				return true
			}
			if !knownPurePackages[packagePath] {
				externalCall = true
			}
		}
		return true
	})

	evidenceList := sortedEvidence(evidence)
	if len(evidenceList) > 0 {
		return Classification{Kind: classificationEdge, Provenance: provenanceInferred, Evidence: evidenceList}, true, externalCall
	}
	return Classification{Kind: classificationUnknown, Provenance: provenanceInferred}, false, externalCall
}

// inspectAssignment recognizes package-state and reference-state writes.
func inspectAssignment(expression ast.Expr, typeInfo *types.Info, evidence map[string]bool) {
	switch target := expression.(type) {
	case *ast.Ident:
		object, ok := typeInfo.Uses[target].(*types.Var)
		if ok && object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
			evidence["writes package-level state"] = true
		}
	case *ast.SelectorExpr:
		evidence["writes object field state"] = true
	case *ast.IndexExpr:
		evidence["writes indexed state"] = true
	}
}

// calledPackage resolves package-qualified function calls without guessing methods.
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

// isEdgePackage applies explicit package evidence rather than name heuristics.
func isEdgePackage(packagePath string) bool {
	for _, prefix := range edgePackagePrefixes {
		if packagePath == prefix || strings.HasPrefix(packagePath, prefix+"/") {
			return true
		}
	}
	return false
}

// classifyFunctions propagates inferred purity only through fully known local calls.
func classifyFunctions(metas map[string]*functionMeta, edges []Edge) {
	for _, edge := range edges {
		if edge.Kind != edgeKindCall {
			continue
		}
		metas[edge.CallerID].localCalls = append(metas[edge.CallerID].localCalls, edge.CalleeID)
	}

	changed := true
	for changed {
		changed = false
		for _, meta := range metas {
			classification := meta.function.Classification
			if classification.Kind != classificationUnknown || meta.directEdge || meta.externalCall {
				continue
			}
			allCalleesPure := true
			for _, calleeID := range meta.localCalls {
				if metas[calleeID].function.Classification.Kind != classificationPure {
					allCalleesPure = false
					break
				}
			}
			if !allCalleesPure {
				continue
			}
			meta.function.Classification = Classification{
				Kind: classificationPure, Provenance: provenanceInferred,
				Evidence: []string{"no visible effects and all analyzed local callees are pure"},
			}
			changed = true
		}
	}

	for _, meta := range metas {
		if meta.function.Classification.Kind != classificationUnknown {
			continue
		}
		if meta.externalCall {
			meta.function.Classification.Evidence = []string{"calls unanalyzed or effect-unknown code"}
			continue
		}
		meta.function.Classification.Evidence = []string{"purity could not be established from the local call graph"}
	}
}

// sortedEvidence makes classification output deterministic.
func sortedEvidence(evidence map[string]bool) []string {
	result := make([]string, 0, len(evidence))
	for item := range evidence {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}
