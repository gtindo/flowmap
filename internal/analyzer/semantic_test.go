package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/gtindo/flowmap/internal/semantic"
)

type backendFunc func(context.Context, semantic.AnalysisRequest) (semantic.Snapshot, error)

func (function backendFunc) Analyze(ctx context.Context, request semantic.AnalysisRequest) (semantic.Snapshot, error) {
	return function(ctx, request)
}

func TestAnalyzeWithBackendPassesRequestAndWrapsErrors(t *testing.T) {
	sentinel := errors.New("backend failed")
	backend := backendFunc(func(_ context.Context, request semantic.AnalysisRequest) (semantic.Snapshot, error) {
		if request.Root != "/work/project" || !reflect.DeepEqual(request.BuildTags, []string{"integration"}) {
			t.Fatalf("request = %#v", request)
		}
		return semantic.Snapshot{}, sentinel
	})

	_, err := AnalyzeWithBackend(context.Background(), Config{Root: "/work/project", BuildTags: []string{"integration"}}, backend)
	if !errors.Is(err, sentinel) || err.Error() != "analyze repository with semantic backend: backend failed" {
		t.Fatalf("AnalyzeWithBackend() error = %v", err)
	}
}

func TestBuildIndexPreservesSemanticIdentityRelationshipsAndJSON(t *testing.T) {
	snapshot := semantic.Snapshot{
		Root: "/work/project",
		Symbols: []semantic.Symbol{
			{
				ID: "caller", Kind: semantic.SymbolFunction, Name: "Run", QualifiedName: "sample.Run", Package: "sample",
				Location: semantic.Location{File: "/work/project/sample.go", Line: 10, EndLine: 14}, Source: "func Run() {}",
				Documentation: "Run coordinates work.\n\nSide Effect (Edge): boundary.",
				Signature:     semantic.Signature{Display: "func(input sample.Input) sample.Output", Parameters: []string{"input sample.Input"}, Results: []string{"sample.Output"}, Contracts: []semantic.Contract{{Name: "sample.Input", Kind: "struct", Fields: []semantic.Field{{Name: "Text", Type: "string"}}}}},
				Facts:         []semantic.Fact{{Kind: semantic.FactExternalCall, Package: "os", Name: "ReadFile"}},
			},
			{ID: "callee", Kind: semantic.SymbolClosure, Name: "Run$1", QualifiedName: "sample.Run$1", Package: "sample", Signature: semantic.Signature{Display: "func()"}},
		},
		Relationships: []semantic.Relationship{{
			FromID: "caller", ToID: "callee", Kind: semantic.RelationshipCall, Location: "/work/project/sample.go:12",
			Provenance: "vta", Precision: "possible", Dynamic: true,
		}},
		Diagnostics: semantic.DiagnosticReport{BuildTags: []string{"integration"}, TotalUnits: 2, FailedUnits: 1, Diagnostics: []semantic.Diagnostic{{Kind: "type", Position: "broken.go:1:1", Message: "broken", Units: []string{"sample/broken"}}}},
	}

	index := buildIndex(snapshot)
	caller := index.Functions["caller"]
	if caller.ID != "caller" || caller.Intent != "Run coordinates work." || caller.Classification.Kind != classificationEdge {
		t.Fatalf("caller enrichment = %#v", caller)
	}
	if !reflect.DeepEqual(caller.Contracts, []Contract{{Name: "sample.Input", Kind: "struct", Fields: []Field{{Name: "Text", Type: "string"}}}}) {
		t.Fatalf("contracts = %#v", caller.Contracts)
	}
	if !index.Functions["callee"].Anonymous {
		t.Fatal("closure did not remain anonymous")
	}
	wantEdge := Edge{CallerID: "caller", CalleeID: "callee", Kind: "call", Dynamic: true, CallSite: "/work/project/sample.go:12"}
	if len(index.Edges) != 1 || index.Edges[0] != wantEdge {
		t.Fatalf("edges = %#v, want %#v", index.Edges, wantEdge)
	}
	if index.LoadReport.Diagnostics[0].Packages[0] != "sample/broken" {
		t.Fatalf("load report = %#v", index.LoadReport)
	}

	encoded, err := json.Marshal(wantEdge)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"caller_id":"caller","callee_id":"callee","kind":"call","dynamic":true,"call_site":"/work/project/sample.go:12"}`; got != want {
		t.Fatalf("edge JSON = %s, want %s", got, want)
	}
}
