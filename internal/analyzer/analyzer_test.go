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
	if !index.LoadReport.HasFailures() || index.LoadReport.FailedPackageVariants == 0 {
		t.Fatalf("Analyze() did not report the intentionally broken neighboring package: %#v", index.LoadReport)
	}
	if !strings.Contains(index.LoadReport.String(), "undefined: missingSymbol") {
		t.Fatalf("load warning omitted the underlying Go diagnostic: %s", index.LoadReport.String())
	}
	run := findFunction(t, index, ".Run")
	normalize := findFunction(t, index, ".Normalize")
	load := findFunction(t, index, ".Load")
	assemblyHook := findFunction(t, index, ".AssemblyHook")
	assemblyPure := findFunction(t, index, ".AssemblyPure")
	callDependency := findFunction(t, index, ".CallDependency")
	if run.Classification.Kind != classificationEdge || run.Classification.Provenance != provenanceAuthored {
		t.Fatalf("Run classification = %#v", run.Classification)
	}
	if normalize.Classification.Kind != classificationPure || normalize.IntentSource != "documentation" {
		t.Fatalf("Normalize metadata = %#v", normalize)
	}
	if load.Classification.Kind != classificationEdge || !strings.Contains(strings.Join(load.Classification.Evidence, " "), "os.ReadFile") {
		t.Fatalf("Load classification = %#v", load.Classification)
	}
	if assemblyHook.Classification.Kind != classificationUnknown || !strings.Contains(strings.Join(assemblyHook.Classification.Evidence, " "), "effect-unknown") {
		t.Fatalf("AssemblyHook classification = %#v", assemblyHook.Classification)
	}
	if assemblyPure.Classification.Kind != classificationPure || assemblyPure.Classification.Provenance != provenanceAuthored {
		t.Fatalf("AssemblyPure classification = %#v", assemblyPure.Classification)
	}
	if callDependency.Classification.Kind != classificationUnknown || !strings.Contains(strings.Join(callDependency.Classification.Evidence, " "), "effect-unknown") {
		t.Fatalf("CallDependency classification = %#v", callDependency.Classification)
	}
	for _, function := range index.Functions {
		if function.Package == "example.com/dependency" {
			t.Fatalf("vendored dependency function included in local graph: %#v", function)
		}
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

func TestAnalyzeReportsDiagnosticsWhenEveryPackageIsBroken(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "testdata", "allbroken")
	_, err := Analyze(context.Background(), Config{Root: root, BuildTags: []string{"integration"}})
	if err == nil {
		t.Fatal("Analyze() succeeded for an entirely broken module")
	}
	message := err.Error()
	for _, expected := range []string{
		"load Go packages: no analyzable packages beneath",
		"[type] broken.go:4:17: undefined: missingSymbol",
		"test -tags='integration' ./...",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("Analyze() error omitted %q:\n%s", expected, message)
		}
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
