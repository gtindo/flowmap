package analyzer

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAnalyzeBuildsTypedFocusedGraph verifies the static index as one coherent behavior.
func TestAnalyzeBuildsTypedFocusedGraph(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	index, err := Analyze(context.Background(), Config{Root: filepath.Join(filepath.Dir(filename), "testdata", "sample")})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	run := findFunction(t, index, ".Run")
	normalize := findFunction(t, index, ".Normalize")
	load := findFunction(t, index, ".Load")
	if run.Classification.Kind != classificationEdge || run.Classification.Provenance != provenanceAuthored {
		t.Fatalf("Run classification = %#v", run.Classification)
	}
	if normalize.Classification.Kind != classificationPure || normalize.IntentSource != "documentation" {
		t.Fatalf("Normalize metadata = %#v", normalize)
	}
	if load.Classification.Kind != classificationEdge || !strings.Contains(strings.Join(load.Classification.Evidence, " "), "os.ReadFile") {
		t.Fatalf("Load classification = %#v", load.Classification)
	}
	if len(run.Parameters) != 3 || len(run.Results) != 2 {
		t.Fatalf("Run contract = params %v results %v", run.Parameters, run.Results)
	}
	if !hasContract(run.Contracts, "sample.Input") || !hasContract(run.Contracts, "sample.Output") || !hasContract(run.Contracts, "sample.Store") {
		t.Fatalf("Run contracts = %#v", run.Contracts)
	}
	graph, err := index.Focus(run.ID, "downstream", 2, false)
	if err != nil {
		t.Fatalf("Focus() error = %v", err)
	}
	if !graphHasNode(graph, normalize.ID) {
		t.Fatalf("focused graph omitted Normalize: %#v", graph.Nodes)
	}
	if got := index.Search("TestNormalize", false, 10); len(got) != 0 {
		t.Fatalf("hidden test search = %#v", got)
	}
	if got := index.Search("TestNormalize", true, 10); len(got) != 1 || !got[0].Test {
		t.Fatalf("visible test search = %#v", got)
	}
}

// findFunction locates one fixture symbol by qualified-name suffix.
func findFunction(t *testing.T, index *Index, suffix string) Function {
	t.Helper()
	for _, function := range index.Functions {
		if strings.HasSuffix(function.QualifiedName, suffix) {
			return function
		}
	}
	t.Fatalf("function ending %q not found", suffix)
	return Function{}
}

// hasContract reports whether contracts contain a named boundary type.
func hasContract(contracts []Contract, name string) bool {
	for _, contract := range contracts {
		if contract.Name == name {
			return true
		}
	}
	return false
}

// graphHasNode reports whether a focused graph contains an ID.
func graphHasNode(graph Graph, id string) bool {
	for _, function := range graph.Nodes {
		if function.ID == id {
			return true
		}
	}
	return false
}
