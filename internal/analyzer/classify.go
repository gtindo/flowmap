package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gtindo/flowmap/internal/semantic"
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
func classifyDirect(documentation string, facts []semantic.Fact) (Classification, bool, bool) {
	normalizedDocumentation := strings.ToLower(documentation)
	if strings.Contains(normalizedDocumentation, "operations (pure)") {
		return Classification{Kind: classificationPure, Provenance: provenanceAuthored, Evidence: []string{"function documentation declares Operations (Pure)"}}, false, false
	}

	if strings.Contains(normalizedDocumentation, "side effect (edge)") || strings.Contains(normalizedDocumentation, "side effects (edge)") {
		return Classification{Kind: classificationEdge, Provenance: provenanceAuthored, Evidence: []string{"function documentation declares Side Effect (Edge)"}}, true, false
	}

	evidence := make(map[string]bool)
	externalCall := false
	for _, fact := range facts {
		switch fact.Kind {
		case semantic.FactDeclarationWithoutBody:
			externalCall = true
		case semantic.FactStartsConcurrentWork:
			evidence["starts a goroutine"] = true
		case semantic.FactChannelSend:
			evidence["sends on a channel"] = true
		case semantic.FactWritesPackageState:
			evidence["writes package-level state"] = true
		case semantic.FactWritesObjectState:
			evidence["writes object field state"] = true
		case semantic.FactWritesIndexedState:
			evidence["writes indexed state"] = true
		case semantic.FactExternalCall:
			if isEdgePackage(fact.Package) {
				evidence[fmt.Sprintf("calls %s.%s", fact.Package, fact.Name)] = true
				continue
			}
			if fact.Package == "" || !knownPurePackages[fact.Package] {
				externalCall = true
			}
		}
	}

	evidenceList := sortedEvidence(evidence)
	if len(evidenceList) > 0 {
		return Classification{Kind: classificationEdge, Provenance: provenanceInferred, Evidence: evidenceList}, true, externalCall
	}

	return Classification{Kind: classificationUnknown, Provenance: provenanceInferred}, false, externalCall
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
