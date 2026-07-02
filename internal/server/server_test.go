package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	for _, expected := range []string{"id=\"view\"", "value=\"simple\"", "id=\"history-back\"", "id=\"history-forward\"", "Back to previous function", "Forward to next function", "button:disabled", "id=\"reset-layout\"", "id=\"zoom-in\"", "id=\"zoom-out\"", "id=\"hand-tool\"", "id=\"detail-resize\"", "role=\"separator\"", "--detail-width", "#detail{width:var(--detail-width,min(520px,46vw));overflow:hidden;padding:0}", "#detail-content{width:100%;height:100%;overflow:auto;padding:25px}", "#close{right:22px;z-index:2}"} {
		if !strings.Contains(page, expected) {
			t.Fatalf("workbench page omitted %s", expected)
		}
	}
	if strings.Contains(page, "id=\"depth\"") {
		t.Fatal("workbench still exposes global depth expansion")
	}
	if strings.Contains(page, "#canvas-wrap{overflow:hidden}") {
		t.Fatal("workbench page disables native graph scrolling")
	}
	styleResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(styleResponse, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	if styleResponse.Code != http.StatusOK || !strings.Contains(styleResponse.Body.String(), "#canvas-wrap{height:calc(100% - 38px);overflow:auto") {
		t.Fatal("workbench stylesheet does not enable graph overflow scrolling")
	}
	scriptResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	script := scriptResponse.Body.String()
	for _, expected := range []string{"startDrag", "pointerMoveThreshold = 4", "Math.hypot", "localStorage.setItem", "resetLayout", "flowmap-layout:v2:", "signedLevels", "step = -1", "centerRootInViewport", "normalizeLayout", "expansionSide", "focusGraph", "focusHistory", "focusHistoryIndex", "navigateHistory", "updateHistoryButtons", "graphGeneration", "options.historyIndex", "expandNode", "collapseNode", "pruneOrphanedExpansions", "zoomGraph", "startPan", "scrollLeft", "scrollTop", "zoomScale", "viewportState", "viewportCenter", "scrollViewportTo", "marginX = wrap.clientWidth", "marginY = wrap.clientHeight", "-marginX / zoomScale", "-marginY / zoomScale", "&depth=1", "highlightGo", "sourceBlock(item.source)", "detailGeneration", "activeDetailID", "setActiveDetail", "detail-selected", "detail-focus-ring", "AbortController", "Loading details…", "Unable to load details", "hideDetail", "item.contracts || []", "item.classification.evidence || []", "expansionActivationWindow = 400", "expansionActivationTimes", "flowmap-detail-width:v1", "startDetailResize", "resizeDetail", "finishDetailResize", "clampDetailWidth", "detailViewportMargin = 48", "Rescan codebase", "rescanCodebase", "showEmptyAfterRescan", "Scanning…", "POST"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("workbench script omitted %s", expected)
		}
	}
	if strings.Contains(script, "group.ondblclick") {
		t.Fatal("workbench still focuses graph nodes on double-click")
	}
	for _, expected := range []string{".token.comment", ".token.keyword", ".token.builtin", ".token.string", ".token.number"} {
		if !strings.Contains(styleResponse.Body.String(), expected) {
			t.Fatalf("workbench stylesheet omitted %s", expected)
		}
	}
	for _, expected := range []string{".node rect.detail-focus-ring", ".node.detail-selected rect.detail-focus-ring", "stroke:#2563eb"} {
		if !strings.Contains(styleResponse.Body.String(), expected) {
			t.Fatalf("workbench stylesheet omitted %s", expected)
		}
	}
}

func TestRescanAtomicallyReplacesIndexAndRejectsOverlap(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	config := analyzer.Config{Root: "/work/project", BuildTags: []string{"integration"}}
	replacement := fixtureIndexWithRoot("replacement", "sample.Replacement")
	app, err := newApp(fixtureIndex(), nil, nil, config, func(_ context.Context, actual analyzer.Config) (*analyzer.Index, error) {
		if actual.Root != config.Root || len(actual.BuildTags) != 1 || actual.BuildTags[0] != "integration" {
			return nil, fmt.Errorf("unexpected analyzer config: %#v", actual)
		}
		calls.Add(1)
		close(started)
		<-release
		return replacement, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
		done <- response
	}()
	<-started

	oldSearch := httptest.NewRecorder()
	app.Handler().ServeHTTP(oldSearch, httptest.NewRequest(http.MethodGet, "/api/search?q=Root", nil))
	if oldSearch.Code != http.StatusOK || !strings.Contains(oldSearch.Body.String(), "sample.Root") {
		t.Fatalf("old index unavailable during rescan: %d %s", oldSearch.Code, oldSearch.Body.String())
	}
	overlap := httptest.NewRecorder()
	app.Handler().ServeHTTP(overlap, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
	if overlap.Code != http.StatusConflict {
		t.Fatalf("overlapping rescan status = %d body = %s", overlap.Code, overlap.Body.String())
	}

	close(release)
	response := <-done
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"function_count":1`) {
		t.Fatalf("rescan response = %d %s", response.Code, response.Body.String())
	}
	newSearch := httptest.NewRecorder()
	app.Handler().ServeHTTP(newSearch, httptest.NewRequest(http.MethodGet, "/api/search?q=Replacement", nil))
	if newSearch.Code != http.StatusOK || !strings.Contains(newSearch.Body.String(), "sample.Replacement") || calls.Load() != 1 {
		t.Fatalf("replacement index not installed: %d %s calls=%d", newSearch.Code, newSearch.Body.String(), calls.Load())
	}
}

func TestFailedRescanKeepsPreviousIndex(t *testing.T) {
	app, err := newApp(fixtureIndex(), nil, nil, analyzer.Config{Root: "/work/project"}, func(context.Context, analyzer.Config) (*analyzer.Index, error) {
		return nil, fmt.Errorf("broken source")
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "broken source") {
		t.Fatalf("failed rescan response = %d %s", response.Code, response.Body.String())
	}
	search := httptest.NewRecorder()
	app.Handler().ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/search?q=Root", nil))
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "sample.Root") {
		t.Fatalf("old index lost after failed rescan: %d %s", search.Code, search.Body.String())
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

func fixtureIndexWithRoot(id string, qualifiedName string) *analyzer.Index {
	root := analyzer.Function{ID: id, Name: qualifiedName, QualifiedName: qualifiedName, Package: "sample", Classification: analyzer.Classification{Kind: "pure"}}
	return &analyzer.Index{Functions: map[string]analyzer.Function{id: root}, Outgoing: map[string][]analyzer.Edge{}, Incoming: map[string][]analyzer.Edge{}}
}
