package server

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
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
	for _, path := range []string{"/api/search?q=Root", "/api/graph?root=root&direction=downstream&depth=1", "/api/functions/root", "/api/git-status"} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body = %s", path, response.Code, response.Body.String())
		}
	}
	graphResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(graphResponse, httptest.NewRequest(http.MethodGet, "/api/graph?root=root&direction=downstream&depth=1", nil))
	if !strings.Contains(graphResponse.Body.String(), `"kind":"call"`) || !strings.Contains(graphResponse.Body.String(), `"anonymous":true`) {
		t.Fatalf("graph omitted edge or closure metadata: %s", graphResponse.Body.String())
	}
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/functions/root/summary", nil))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("disabled summary status = %d", response.Code)
	}
	gitResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(gitResponse, httptest.NewRequest(http.MethodGet, "/api/git-status", nil))
	var gitStatus analyzer.GitSnapshot
	if err := json.Unmarshal(gitResponse.Body.Bytes(), &gitStatus); err != nil || gitStatus.Branch != "main" || len(gitStatus.ChangedFunctions) != 1 {
		t.Fatalf("Git status = %#v, %v", gitStatus, err)
	}
	detailResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/api/functions/root", nil))
	if !strings.Contains(detailResponse.Body.String(), `"change":{"kind":"updated"`) || !strings.Contains(detailResponse.Body.String(), `"diff":`) {
		t.Fatalf("changed function detail = %s", detailResponse.Body.String())
	}
}

// TestHandlerServesNavigableGraphViews verifies both interactive workbench modes.
func TestHandlerServesNavigableGraphViews(t *testing.T) {
	app, _ := New(fixtureIndex(), nil, nil)
	pageResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	page := pageResponse.Body.String()
	for _, expected := range []string{"class=\"app-header\"", "class=\"brand\"", "class=\"search-wrap\"", "class=\"controls\"", "aria-label=\"Navigation\"", "aria-label=\"Graph options\"", "aria-label=\"Canvas tools\"", "aria-label=\"Utilities\"", "id=\"view\"", "value=\"simple\"", "id=\"history-back\"", "id=\"history-forward\"", "Back to previous function", "Forward to next function", "id=\"reset-layout\"", "id=\"zoom-in\"", "id=\"zoom-out\"", "id=\"hand-tool\" class=\"icon-button active\"", "aria-label=\"Pan tool\" aria-pressed=\"true\"", "id=\"canvas\" class=\"hand\"", "id=\"git-review\"", "id=\"git-branch\"", "id=\"changes-button\"", "id=\"changes-menu\"", "id=\"rescan\"", "id=\"detail-resize\"", "role=\"separator\""} {
		if !strings.Contains(page, expected) {
			t.Fatalf("workbench page omitted %s", expected)
		}
	}
	workspaceStart := strings.Index(page, `<section id="workspace" class="hidden">`)
	legendStart := strings.Index(page, `<div id="legend">`)
	navigationStart := strings.Index(page, `aria-label="Navigation"`)
	graphOptionsStart := strings.Index(page, `aria-label="Graph options"`)
	canvasStart := strings.Index(page, `<div id="canvas-wrap">`)
	if workspaceStart < 0 || legendStart < workspaceStart || navigationStart < legendStart || graphOptionsStart < navigationStart || canvasStart < graphOptionsStart {
		t.Fatal("navigation and graph options are not nested in the hidden graph context bar")
	}
	if strings.Contains(page, "<style>") {
		t.Fatal("workbench page still contains inline component styles")
	}
	if strings.Contains(page, "id=\"depth\"") {
		t.Fatal("workbench still exposes global depth expansion")
	}
	styleResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(styleResponse, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	style := styleResponse.Body.String()
	for _, expected := range []string{"--surface:", "--context-bar-height: 40px", ":root[data-theme=\"dark\"]", ".app-header", ".control-group", ".context-controls", ".context-controls .control-group { min-height: 30px", "margin-left: auto", ".git-review", ".changes-menu", ".change-item", ".reset-reviewed", ".reviewed-list-badge", ".reviewed-node-badge", ".reviewed-action", "--reviewed:", ".diff-addition", ".diff-deletion", ".node .name.new { fill: var(--change-new); }", ".node .name.updated { fill: var(--change-updated); }", "#edges path.dependency", "stroke-dasharray: 2 5", "#canvas-wrap", "overflow: auto", "#detail", "top: var(--context-bar-height)", "height: calc(100% - var(--context-bar-height))", "width: var(--detail-width", "@media (max-width: 1480px)", "grid-template-columns: auto minmax(300px, 1fr)", "grid-template-areas: \"brand search\" \"controls controls\""} {
		if !strings.Contains(style, expected) {
			t.Fatalf("workbench stylesheet omitted %s", expected)
		}
	}
	scriptResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	script := scriptResponse.Body.String()
	for _, expected := range []string{"startDrag", "pointerMoveThreshold = 4", "Math.hypot", "localStorage.setItem", "resetLayout", "flowmap-layout:v2:", "signedLevels", "step = -1", "centerRootInViewport", "normalizeLayout", "expansionSide", "focusGraph", "focusHistory", "focusHistoryIndex", "navigateHistory", "updateHistoryButtons", "graphGeneration", "options.historyIndex", "expandNode", "collapseNode", "pruneOrphanedExpansions", "zoomGraph", "startPan", "scrollLeft", "scrollTop", "zoomScale", "viewportState", "viewportCenter", "scrollViewportTo", "marginX = wrap.clientWidth", "marginY = wrap.clientHeight", "-marginX / zoomScale", "-marginY / zoomScale", "&depth=1", "highlightGo", "sourceBlock(item.source)", "source-heading", "diffBlock", "diff-addition", "loadGitStatus", "/api/git-status", "visibleChangedFunctions", "toggleChangesMenu", "hideChangesMenu", "renderGitStatus(result.git_status)", "flowmap-reviewed-functions:v1", "loadReviewedFunctions", "saveReviewedFunctions", "resetReviewedFunctions", "window.confirm", "function_ids", "reviewedRevision", "reviewedFunctionIDs", "addReviewedNodeBadge", "Mark reviewed", "Mark unreviewed", "item.change", "const nameClass = \"name\" + (changeKind ? \" \" + changeKind : \"\")", "in Git diff", "edge.kind === \"dependency\"", "Function dependency", "Show diff", "Show source", "aria-pressed", "detailGeneration", "activeDetailID", "setActiveDetail", "detail-selected", "detail-focus-ring", "AbortController", "Loading details…", "Unable to load details", "hideDetail", "item.contracts || []", "item.classification.evidence || []", "expansionActivationWindow = 400", "expansionActivationTimes", "flowmap-detail-width:v1", "startDetailResize", "resizeDetail", "finishDetailResize", "clampDetailWidth", "detailViewportMargin = 48", "rescanCodebase", "showEmptyAfterRescan", "Scanning…", "POST"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("workbench script omitted %s", expected)
		}
	}
	if strings.Contains(script, "group.ondblclick") {
		t.Fatal("workbench still focuses graph nodes on double-click")
	}
	for _, expected := range []string{".token.comment", ".token.keyword", ".token.builtin", ".token.string", ".token.number"} {
		if !strings.Contains(style, expected) {
			t.Fatalf("workbench stylesheet omitted %s", expected)
		}
	}
	for _, expected := range []string{".node rect.detail-focus-ring", ".node.detail-selected rect.detail-focus-ring", "stroke: var(--focus)"} {
		if !strings.Contains(style, expected) {
			t.Fatalf("workbench stylesheet omitted %s", expected)
		}
	}
}

func TestHandlerServesPersistentSystemAwareThemes(t *testing.T) {
	app, _ := New(fixtureIndex(), nil, nil)
	pageResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	page := pageResponse.Body.String()
	for _, expected := range []string{"flowmap-theme:v1", "matchMedia(\"(prefers-color-scheme: dark)\")", "dataset.theme = resolved", "value=\"system\"", "value=\"light\"", "value=\"dark\""} {
		if !strings.Contains(page, expected) {
			t.Fatalf("theme bootstrap omitted %s", expected)
		}
	}

	scriptResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	script := scriptResponse.Body.String()
	for _, expected := range []string{"flowmap-theme:v1", "readThemePreference", "applyTheme", "selectTheme", "systemTheme.addEventListener(\"change\"", "localStorage.setItem(themePreferenceKey", "dataset.themePreference"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("theme behavior omitted %s", expected)
		}
	}
}

func TestHandlerServesInstallablePWA(t *testing.T) {
	app, _ := New(fixtureIndex(), nil, nil)
	handler := app.Handler()

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	page := pageResponse.Body.String()
	for _, expected := range []string{
		`rel="manifest" href="/manifest.webmanifest"`,
		`rel="icon" href="/favicon.svg" type="image/svg+xml"`,
		`rel="apple-touch-icon" href="/icon-512.png"`,
		`name="theme-color" content="#f8fafc"`,
		`name="theme-color" content="#171b22"`,
	} {
		if !strings.Contains(page, expected) {
			t.Fatalf("PWA page metadata omitted %s", expected)
		}
	}

	manifestResponse := httptest.NewRecorder()
	handler.ServeHTTP(manifestResponse, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if manifestResponse.Code != http.StatusOK || !strings.HasPrefix(manifestResponse.Header().Get("Content-Type"), "application/manifest+json") {
		t.Fatalf("manifest response = %d %q", manifestResponse.Code, manifestResponse.Header().Get("Content-Type"))
	}
	var manifest struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		StartURL        string `json:"start_url"`
		Scope           string `json:"scope"`
		Display         string `json:"display"`
		BackgroundColor string `json:"background_color"`
		ThemeColor      string `json:"theme_color"`
		Icons           []struct {
			Source  string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(manifestResponse.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != "/" || manifest.Name != "Flowmap" || manifest.StartURL != "/" || manifest.Scope != "/" || manifest.Display != "standalone" || manifest.BackgroundColor != "#eef1f5" || manifest.ThemeColor != "#f8fafc" {
		t.Fatalf("manifest metadata = %#v", manifest)
	}
	if len(manifest.Icons) != 2 {
		t.Fatalf("manifest icons = %#v", manifest.Icons)
	}
	faviconResponse := httptest.NewRecorder()
	handler.ServeHTTP(faviconResponse, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if faviconResponse.Code != http.StatusOK || !strings.HasPrefix(faviconResponse.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(faviconResponse.Body.String(), `<svg xmlns="http://www.w3.org/2000/svg"`) {
		t.Fatalf("favicon response = %d %q %s", faviconResponse.Code, faviconResponse.Header().Get("Content-Type"), faviconResponse.Body.String())
	}
	for index, expectedSize := range []int{192, 512} {
		expectedPath := fmt.Sprintf("/icon-%d.png", expectedSize)
		icon := manifest.Icons[index]
		if icon.Source != expectedPath || icon.Sizes != fmt.Sprintf("%dx%d", expectedSize, expectedSize) || icon.Type != "image/png" || icon.Purpose != "any maskable" {
			t.Fatalf("manifest icon %d = %#v", expectedSize, icon)
		}
		iconResponse := httptest.NewRecorder()
		handler.ServeHTTP(iconResponse, httptest.NewRequest(http.MethodGet, expectedPath, nil))
		config, err := png.DecodeConfig(iconResponse.Body)
		if iconResponse.Code != http.StatusOK || !strings.HasPrefix(iconResponse.Header().Get("Content-Type"), "image/png") || err != nil || config.Width != expectedSize || config.Height != expectedSize {
			t.Fatalf("icon %s = status %d, config %#v, error %v", expectedPath, iconResponse.Code, config, err)
		}
	}

	scriptResponse := httptest.NewRecorder()
	handler.ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if !strings.Contains(scriptResponse.Body.String(), `navigator.serviceWorker.register("/sw.js")`) {
		t.Fatal("app script does not register the root service worker")
	}
	workerResponse := httptest.NewRecorder()
	handler.ServeHTTP(workerResponse, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	worker := workerResponse.Body.String()
	for _, expected := range []string{
		`const fallbackAssets = ["/offline.html"]`,
		`event.request.mode !== "navigate"`,
		`fetch(event.request).catch(() => caches.match("/offline.html"))`,
		`key.startsWith("flowmap-offline-") && key !== fallbackCache`,
		`self.clients.claim()`,
	} {
		if !strings.Contains(worker, expected) {
			t.Fatalf("service worker omitted %s", expected)
		}
	}
	offlineResponse := httptest.NewRecorder()
	handler.ServeHTTP(offlineResponse, httptest.NewRequest(http.MethodGet, "/offline.html", nil))
	if offlineResponse.Code != http.StatusOK || !strings.Contains(offlineResponse.Body.String(), "Flowmap isn’t running") || !strings.Contains(offlineResponse.Body.String(), "flowmap serve /path/to/go/project") {
		t.Fatalf("offline response = %d %s", offlineResponse.Code, offlineResponse.Body.String())
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"function_count":1`) || !strings.Contains(response.Body.String(), `"git_status"`) {
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
	gitStatus := httptest.NewRecorder()
	app.Handler().ServeHTTP(gitStatus, httptest.NewRequest(http.MethodGet, "/api/git-status", nil))
	if gitStatus.Code != http.StatusOK || !strings.Contains(gitStatus.Body.String(), `"branch":"main"`) {
		t.Fatalf("old Git snapshot lost after failed rescan: %d %s", gitStatus.Code, gitStatus.Body.String())
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
	root := analyzer.Function{ID: "root", QualifiedName: "sample.Root", Package: "sample", File: "/work/sample.go", Line: 10, Classification: analyzer.Classification{Kind: "pure"}, Change: &analyzer.FunctionChange{Kind: "updated", Diff: "--- a/sample.go\n+++ b/sample.go\n@@ -1 +1 @@\n-old\n+new\n"}}
	child := analyzer.Function{ID: "child", Name: "Root$1", QualifiedName: "sample.Root$1", Package: "sample", Anonymous: true, Classification: analyzer.Classification{Kind: "unknown"}}
	edge := analyzer.Edge{CallerID: "root", CalleeID: "child", Kind: "call"}
	gitStatus := analyzer.GitSnapshot{Available: true, Branch: "main", Revision: "1234567890", ChangedFunctions: []analyzer.ChangedFunction{{ID: "root", QualifiedName: "sample.Root", Package: "sample", File: root.File, Line: root.Line, Kind: "updated"}}}
	return &analyzer.Index{Functions: map[string]analyzer.Function{"root": root, "child": child}, Edges: []analyzer.Edge{edge}, Outgoing: map[string][]analyzer.Edge{"root": {edge}}, Incoming: map[string][]analyzer.Edge{"child": {edge}}, Git: gitStatus}
}

func fixtureIndexWithRoot(id string, qualifiedName string) *analyzer.Index {
	root := analyzer.Function{ID: id, Name: qualifiedName, QualifiedName: qualifiedName, Package: "sample", Classification: analyzer.Classification{Kind: "pure"}}
	return &analyzer.Index{Functions: map[string]analyzer.Function{id: root}, Outgoing: map[string][]analyzer.Edge{}, Incoming: map[string][]analyzer.Edge{}}
}
