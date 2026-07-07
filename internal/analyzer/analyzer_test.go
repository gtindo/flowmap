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
	startWorker := findFunction(t, index, ".StartWorker")
	startHTTPServer := findFunction(t, index, ".startHTTPServer")
	handleSomething := findFunction(t, index, ".HandleSomething")
	callbackOwnerHandle := findFunction(t, index, ".Handle")
	genericCallback := findFunction(t, index, ".GenericCallback")
	registerCallbacks := findFunction(t, index, ".RegisterCallbacks")
	sideEffectCallback := findFunction(t, index, ".SideEffectCallback")
	registerSideEffectCallback := findFunction(t, index, ".RegisterSideEffectCallback")
	returnNamedCallback := findFunction(t, index, ".ReturnNamedCallback")
	returnMethodCallback := findFunction(t, index, ".ReturnMethodCallback")
	returnGenericCallback := findFunction(t, index, ".ReturnGenericCallback")
	returnWrappedCallback := findFunction(t, index, ".ReturnWrappedCallback")
	returnClosureCallback := findFunction(t, index, ".ReturnClosureCallback")
	buildCallback := findFunction(t, index, ".BuildCallback")
	returnCalledCallback := findFunction(t, index, ".ReturnCalledCallback")
	executeReturningCallback := findFunction(t, index, ".ExecuteReturningCallback")
	returningCallbackTarget := findFunction(t, index, ".ReturningCallbackTarget")
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
	workerClosure := findAnonymousFunction(t, index, ".StartWorker$1")
	if !strings.Contains(workerClosure.Source, "serverErrors <- startHTTPServer()") || workerClosure.Classification.Kind != classificationEdge {
		t.Fatalf("worker closure metadata = %#v", workerClosure)
	}
	assertEdge(t, index, startWorker.ID, workerClosure.ID, edgeKindCall)
	assertEdge(t, index, workerClosure.ID, startHTTPServer.ID, edgeKindCall)
	assertEdge(t, index, registerCallbacks.ID, handleSomething.ID, edgeKindDependency)
	assertEdge(t, index, registerCallbacks.ID, callbackOwnerHandle.ID, edgeKindDependency)
	assertEdge(t, index, registerCallbacks.ID, genericCallback.ID, edgeKindDependency)
	registerClosure := findAnonymousFunction(t, index, ".RegisterCallbacks$1")
	assertEdge(t, index, registerCallbacks.ID, registerClosure.ID, edgeKindDependency)
	assertEdge(t, index, registerSideEffectCallback.ID, sideEffectCallback.ID, edgeKindDependency)
	assertEdge(t, index, returnNamedCallback.ID, handleSomething.ID, edgeKindDependency)
	assertEdge(t, index, returnMethodCallback.ID, callbackOwnerHandle.ID, edgeKindDependency)
	assertEdge(t, index, returnGenericCallback.ID, genericCallback.ID, edgeKindDependency)
	assertEdge(t, index, returnWrappedCallback.ID, handleSomething.ID, edgeKindDependency)
	returnedClosure := findAnonymousFunction(t, index, ".ReturnClosureCallback$1")
	assertEdge(t, index, returnClosureCallback.ID, returnedClosure.ID, edgeKindDependency)
	assertEdge(t, index, returnCalledCallback.ID, buildCallback.ID, edgeKindCall)
	assertNoEdge(t, index, returnCalledCallback.ID, buildCallback.ID, edgeKindDependency)
	assertDynamicEdge(t, index, executeReturningCallback.ID, returningCallbackTarget.ID, edgeKindCall)
	if registerSideEffectCallback.Classification.Kind != classificationPure {
		t.Fatalf("dependency affected caller classification = %#v", registerSideEffectCallback.Classification)
	}
	if got := index.Search("StartWorker", true, 10); len(got) != 1 || got[0].ID != startWorker.ID {
		t.Fatalf("anonymous function leaked into search = %#v", got)
	}
	workerGraph, err := index.Focus(startWorker.ID, "downstream", 2, false)
	if err != nil || !graphHasNode(workerGraph, workerClosure.ID) || !graphHasNode(workerGraph, startHTTPServer.ID) {
		t.Fatalf("worker graph = %#v, %v", workerGraph, err)
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

func findAnonymousFunction(t *testing.T, index *Index, suffix string) Function {
	t.Helper()
	for _, function := range index.Functions {
		if function.Anonymous && strings.HasSuffix(function.QualifiedName, suffix) {
			return function
		}
	}
	t.Fatalf("anonymous function ending %q not found", suffix)
	return Function{}
}

func assertEdge(t *testing.T, index *Index, callerID string, calleeID string, kind string) {
	t.Helper()
	for _, edge := range index.Edges {
		if edge.CallerID == callerID && edge.CalleeID == calleeID && edge.Kind == kind {
			return
		}
	}
	t.Fatalf("edge %s -> %s (%s) not found in %#v", callerID, calleeID, kind, index.Edges)
}

func assertNoEdge(t *testing.T, index *Index, callerID string, calleeID string, kind string) {
	t.Helper()
	for _, edge := range index.Edges {
		if edge.CallerID == callerID && edge.CalleeID == calleeID && edge.Kind == kind {
			t.Fatalf("unexpected edge %s -> %s (%s) found in %#v", callerID, calleeID, kind, index.Edges)
		}
	}
}

func assertDynamicEdge(t *testing.T, index *Index, callerID string, calleeID string, kind string) {
	t.Helper()
	for _, edge := range index.Edges {
		if edge.CallerID == callerID && edge.CalleeID == calleeID && edge.Kind == kind && edge.Dynamic {
			return
		}
	}
	t.Fatalf("dynamic edge %s -> %s (%s) not found in %#v", callerID, calleeID, kind, index.Edges)
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
