package gobackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/gtindo/flowmap/internal/semantic"
)

var _ semantic.Backend = Backend{}

func TestBackendPreservesStableIDsAndExactRelationships(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "analyzer", "testdata", "sample"))
	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	run := findSymbol(t, snapshot.Symbols, "sample.Run")
	normalize := findSymbol(t, snapshot.Symbols, "sample.Normalize")
	callbackHandle := findSymbol(t, snapshot.Symbols, "sample.(sample.CallbackOwner).Handle")
	internal := findSymbol(t, snapshot.Symbols, "sample.startHTTPServer")
	wantRunID := expectedID("example.com/sample|sample.Run|" + filepath.Join(root, "sample.go") + ":35")
	if run.ID != wantRunID {
		t.Fatalf("Run ID = %q, want %q", run.ID, wantRunID)
	}
	if run.Signature.Display != "func(ctx context.Context, store sample.Store, input sample.Input) (sample.Output, error)" || len(run.Signature.Contracts) != 5 {
		t.Fatalf("Run signature = %#v", run.Signature)
	}
	if !run.Public || !callbackHandle.Public || internal.Public {
		t.Fatalf("public visibility = Run:%t Handle:%t startHTTPServer:%t", run.Public, callbackHandle.Public, internal.Public)
	}

	wantSite := filepath.Join(root, "sample.go") + ":36"
	for _, relationship := range snapshot.Relationships {
		if relationship.FromID == run.ID && relationship.ToID == normalize.ID && relationship.Kind == semantic.RelationshipCall {
			if relationship.Dynamic || relationship.Location != wantSite || relationship.Provenance != "ssa" || relationship.Precision != "exact" {
				t.Fatalf("Run -> Normalize relationship = %#v", relationship)
			}
			return
		}
	}
	t.Fatal("Run -> Normalize relationship not found")
}

func TestBackendMarksVTADispatchCandidatesDynamic(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "testdata", "dynamic")
	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	caller := findSymbol(t, snapshot.Symbols, "dynamic.Call")
	callee := findSymbol(t, snapshot.Symbols, "dynamic.(dynamic.Worker).Run")
	for _, relationship := range snapshot.Relationships {
		if relationship.FromID == caller.ID && relationship.ToID == callee.ID {
			if !relationship.Dynamic || relationship.Provenance != "vta" || relationship.Precision != "possible" {
				t.Fatalf("dynamic relationship = %#v", relationship)
			}
			return
		}
	}
	t.Fatal("VTA dispatch relationship not found")
}

func TestBackendTracksReturnedFunctionValuesAsDependencies(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "analyzer", "testdata", "sample"))
	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	returnNamed := findSymbol(t, snapshot.Symbols, "sample.ReturnNamedCallback")
	handleSomething := findSymbol(t, snapshot.Symbols, "sample.HandleSomething")
	returnCalled := findSymbol(t, snapshot.Symbols, "sample.ReturnCalledCallback")
	buildCallback := findSymbol(t, snapshot.Symbols, "sample.BuildCallback")

	assertRelationship(t, snapshot.Relationships, returnNamed.ID, handleSomething.ID, semantic.RelationshipDependency)
	assertRelationship(t, snapshot.Relationships, returnCalled.ID, buildCallback.ID, semantic.RelationshipCall)
	assertNoRelationship(t, snapshot.Relationships, returnCalled.ID, buildCallback.ID, semantic.RelationshipDependency)
}

func TestBackendTracksInvokedFunctionParametersAsDynamicCalls(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "analyzer", "testdata", "sample"))
	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	dispatch := findSymbol(t, snapshot.Symbols, "sample.ExecuteReturningCallback")
	target := findSymbol(t, snapshot.Symbols, "sample.ReturningCallbackTarget")
	for _, relationship := range snapshot.Relationships {
		if relationship.FromID == dispatch.ID && relationship.ToID == target.ID && relationship.Kind == semantic.RelationshipCall {
			if !relationship.Dynamic || relationship.Provenance != "vta" || relationship.Precision != "possible" {
				t.Fatalf("function-parameter relationship = %#v", relationship)
			}
			return
		}
	}
	t.Fatalf("dynamic function-parameter relationship %s -> %s not found", dispatch.ID, target.ID)
}

func assertRelationship(t *testing.T, relationships []semantic.Relationship, fromID string, toID string, kind string) {
	t.Helper()
	for _, relationship := range relationships {
		if relationship.FromID == fromID && relationship.ToID == toID && relationship.Kind == kind {
			return
		}
	}
	t.Fatalf("relationship %s -> %s (%s) not found in %#v", fromID, toID, kind, relationships)
}

func assertNoRelationship(t *testing.T, relationships []semantic.Relationship, fromID string, toID string, kind string) {
	t.Helper()
	for _, relationship := range relationships {
		if relationship.FromID == fromID && relationship.ToID == toID && relationship.Kind == kind {
			t.Fatalf("unexpected relationship %s -> %s (%s) found in %#v", fromID, toID, kind, relationships)
		}
	}
}

func findSymbol(t *testing.T, symbols []semantic.Symbol, qualifiedName string) semantic.Symbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.QualifiedName == qualifiedName {
			return symbol
		}
	}
	t.Fatalf("symbol %q not found", qualifiedName)
	return semantic.Symbol{}
}

func expectedID(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:16])
}

func TestBackendMarksDynamicCallsWithoutAnalyzedCalleesAsUnknown(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "testdata", "unresolved")
	snapshot, err := (Backend{}).Analyze(context.Background(), semantic.AnalysisRequest{Root: root})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	unknownCall := semantic.Fact{Kind: semantic.FactExternalCall}
	tests := []struct {
		name string
		want bool
	}{
		{name: "unresolved.Describe", want: true},
		{name: "unresolved.Apply", want: true},
		{name: "unresolved.Length", want: false},
	}
	for _, test := range tests {
		symbol := findSymbol(t, snapshot.Symbols, test.name)
		if got := slices.Contains(symbol.Facts, unknownCall); got != test.want {
			t.Errorf("%s unknown external call = %t, want %t; facts = %#v", test.name, got, test.want, symbol.Facts)
		}
	}
}

func TestIsDependencySourceSelectsOnlyToolchainAndModuleCacheFiles(t *testing.T) {
	root := filepath.FromSlash("/work/module")
	toolchain := toolchainEnvironment{Root: filepath.FromSlash("/go/root"), ModuleCache: filepath.FromSlash("/go/mod")}
	tests := []struct {
		filename string
		want     bool
	}{
		{filename: "/go/root/src/net/http/server.go", want: true},
		{filename: "/go/mod/example.com/library@v1.0.0/library.go", want: true},
		{filename: "/work/module/main.go", want: false},
		{filename: "/work/sibling/replaced.go", want: false},
		{filename: "/cache/go-build/ab/generated-testmain", want: false},
	}
	for _, test := range tests {
		if got := isDependencySource(root, toolchain, filepath.FromSlash(test.filename)); got != test.want {
			t.Errorf("isDependencySource(%s) = %t, want %t", test.filename, got, test.want)
		}
	}

	nested := toolchainEnvironment{ModuleCache: filepath.FromSlash("/go/mod")}
	if isDependencySource(filepath.FromSlash("/go/mod/example.com/analyzed@v1.0.0"), nested, filepath.FromSlash("/go/mod/example.com/analyzed@v1.0.0/file.go")) {
		t.Error("isDependencySource() stripped a file beneath the analysis root")
	}
}
