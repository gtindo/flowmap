package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gtindo/flowmap/internal/analyzer"
)

// TestHandlerServesSearchGraphAndDetails verifies the browser API boundaries.
func TestHandlerServesSearchGraphAndDetails(t *testing.T) {
	index := fixtureIndex()
	app, err := New(index, nil, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, path := range []string{"/api/search?q=Root", "/api/graph?root=root&direction=downstream&depth=1", "/api/functions/root"} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body = %s", path, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/functions/root/summary", nil))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("disabled summary status = %d", response.Code)
	}
}

// TestHandlerServesNavigableGraphViews verifies both interactive workbench modes.
func TestHandlerServesNavigableGraphViews(t *testing.T) {
	app, _ := New(fixtureIndex(), nil, nil)
	pageResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	page := pageResponse.Body.String()
	for _, expected := range []string{"id=\"view\"", "value=\"simple\"", "id=\"reset-layout\"", "id=\"zoom-in\"", "id=\"zoom-out\"", "id=\"hand-tool\""} {
		if !strings.Contains(page, expected) {
			t.Fatalf("workbench page omitted %s", expected)
		}
	}
	if strings.Contains(page, "id=\"depth\"") {
		t.Fatal("workbench still exposes global depth expansion")
	}
	scriptResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	script := scriptResponse.Body.String()
	for _, expected := range []string{"startDrag", "localStorage.setItem", "resetLayout", "flowmap-layout:", "focusGraph", "expandNode", "collapseNode", "pruneOrphanedExpansions", "zoomGraph", "startPan", "&depth=1"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("workbench script omitted %s", expected)
		}
	}
}

// TestCommandSummarizerAndContentCache verifies opt-in generation and source-hash invalidation.
func TestCommandSummarizerAndContentCache(t *testing.T) {
	summarizer := CommandSummarizer{Command: "printf \"{\\\"summary\\\":\\\"generated intent\\\"}\""}
	request := SummaryRequest{QualifiedName: "sample.Root", Signature: "func()", Source: "one"}
	summary, err := summarizer.Summarize(context.Background(), request)
	if err != nil || summary != "generated intent" {
		t.Fatalf("Summarize() = %q, %v", summary, err)
	}
	cache := &SummaryCache{directory: t.TempDir()}
	if err := cache.Put(summarizer.Identity(), request, summary); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if cached, ok := cache.Get(summarizer.Identity(), request); !ok || cached != summary {
		t.Fatalf("Get() = %q, %t", cached, ok)
	}
	request.Source = "two"
	if _, ok := cache.Get(summarizer.Identity(), request); ok {
		t.Fatal("changed source reused stale summary")
	}
	if _, err := (CommandSummarizer{Command: "false"}).Summarize(context.Background(), request); err == nil {
		t.Fatal("provider failure was not returned")
	}
}

// TestSummaryEndpointMarksGeneratedIntent verifies the successful API envelope.
func TestSummaryEndpointMarksGeneratedIntent(t *testing.T) {
	cache := &SummaryCache{directory: t.TempDir()}
	app, _ := New(fixtureIndex(), CommandSummarizer{Command: "printf \"{\\\"summary\\\":\\\"intent\\\"}\""}, cache)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/functions/root/summary", strings.NewReader("")))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var result SummaryResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Source != "generated" {
		t.Fatalf("result = %#v, %v", result, err)
	}
}

// fixtureIndex returns a minimal immutable graph for HTTP tests.
func fixtureIndex() *analyzer.Index {
	root := analyzer.Function{ID: "root", QualifiedName: "sample.Root", Package: "sample", Classification: analyzer.Classification{Kind: "pure"}}
	child := analyzer.Function{ID: "child", QualifiedName: "sample.Child", Package: "sample", Classification: analyzer.Classification{Kind: "unknown"}}
	edge := analyzer.Edge{CallerID: "root", CalleeID: "child"}
	return &analyzer.Index{Functions: map[string]analyzer.Function{"root": root, "child": child}, Edges: []analyzer.Edge{edge}, Outgoing: map[string][]analyzer.Edge{"root": {edge}}, Incoming: map[string][]analyzer.Edge{"child": {edge}}}
}
