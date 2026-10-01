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
	"time"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/engine"
	"github.com/gtindo/flowmap/internal/protocol"
)

// TestHandlerServesSearchGraphAndDetails verifies the browser API boundaries.
func TestHandlerServesSearchGraphAndDetails(t *testing.T) {
	app := newScannedApp(t, fixtureIndex(), engine.Options{})
	for _, path := range []string{"/api/search?q=Root", "/api/graph?root=root&direction=downstream&depth=1", "/api/functions/root", "/api/git-status"} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body = %s", path, response.Code, response.Body.String())
		}
	}
	graphResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(graphResponse, httptest.NewRequest(http.MethodGet, "/api/graph?root=root&direction=downstream&depth=1", nil))
	if !strings.Contains(graphResponse.Body.String(), `"kind":"call"`) || !strings.Contains(graphResponse.Body.String(), `"anonymous":true`) || !strings.Contains(graphResponse.Body.String(), `"public":true`) {
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
	if err := json.Unmarshal(gitResponse.Body.Bytes(), &gitStatus); err != nil || gitStatus.Branch != "main" || len(gitStatus.ChangedFunctions) != 1 || gitStatus.ChangedFunctions[0].LeafDescendantCount != 2 {
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
	app := newScannedApp(t, fixtureIndex(), engine.Options{})
	pageResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	page := pageResponse.Body.String()
	for _, expected := range []string{"class=\"app-header\"", "class=\"brand\"", "class=\"search-wrap\"", "class=\"controls\"", "aria-label=\"Navigation\"", "aria-label=\"Graph options\"", "aria-label=\"Canvas tools\"", "aria-label=\"Utilities\"", "id=\"view\"", "value=\"simple\"", "id=\"public-only\"", "Public only", "id=\"history-back\"", "id=\"history-forward\"", "Back to previous function", "Forward to next function", "id=\"reset-layout\"", "id=\"zoom-in\"", "id=\"zoom-out\"", "id=\"hand-tool\" class=\"icon-button active\"", "aria-label=\"Pan tool\" aria-pressed=\"true\"", "id=\"canvas\" class=\"hand\"", "id=\"git-review\"", "id=\"git-branch\"", "id=\"changes-button\"", "id=\"changes-menu\"", "id=\"rescan\"", "id=\"detail-resize\"", "role=\"separator\""} {
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
	if !strings.Contains(style, ".change-leaf-badge") {
		t.Fatal("workbench stylesheet omitted changed leaf badge")
	}
	scriptResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(scriptResponse, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	script := scriptResponse.Body.String()
	for _, expected := range []string{"startDrag", "pointerMoveThreshold = 4", "Math.hypot", "localStorage.setItem", "resetLayout", "flowmap-layout:v2:", "flowmap-public-only:v1", "readPublicOnlyPreference", "updatePublicOnlyGraph", "graphForDisplay", "item.id === graph.root || item.public", "signedLevels", "step = -1", "centerRootInViewport", "normalizeLayout", "expansionSide", "focusGraph", "focusHistory", "focusHistoryIndex", "navigateHistory", "updateHistoryButtons", "graphGeneration", "options.historyIndex", "expandNode", "collapseNode", "pruneOrphanedExpansions", "zoomGraph", "startPan", "scrollLeft", "scrollTop", "zoomScale", "viewportState", "viewportCenter", "scrollViewportTo", "marginX = wrap.clientWidth", "marginY = wrap.clientHeight", "-marginX / zoomScale", "-marginY / zoomScale", "&depth=1", "highlightSource", "sourceBlock(item.source, item.language)", "source-heading", "diffBlock", "diff-addition", "loadGitStatus", "/api/git-status", "visibleChangedFunctions", "toggleChangesMenu", "hideChangesMenu", "renderGitStatus(result.git_status)", "flowmap-reviewed-functions:v1", "loadReviewedFunctions", "saveReviewedFunctions", "resetReviewedFunctions", "window.confirm", "function_ids", "reviewedRevision", "reviewedFunctionIDs", "addReviewedNodeBadge", "Mark reviewed", "Mark unreviewed", "item.change", "const nameClass = \"name\" + (changeKind ? \" \" + changeKind : \"\")", "in Git diff", "edge.kind === \"dependency\"", "Function dependency", "Show diff", "Show source", "aria-pressed", "detailGeneration", "activeDetailID", "setActiveDetail", "detail-focus-ring", "AbortController", "Loading details…", "Unable to load details", "hideDetail", "item.contracts || []", "item.classification.evidence || []", "expansionActivationWindow = 400", "expansionActivationTimes", "flowmap-detail-width:v1", "startDetailResize", "resizeDetail", "finishDetailResize", "clampDetailWidth", "detailViewportMargin = 48", "rescanCodebase", "showEmptyAfterRescan", "Scanning…", "POST"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("workbench script omitted %s", expected)
		}
	}
	for _, expected := range []string{"leaf_descendant_count", "change-leaf-badge", "changed leaf descendants"} {
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
	app := newScannedApp(t, fixtureIndex(), engine.Options{})
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
	app := newScannedApp(t, fixtureIndex(), engine.Options{})
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
	analyze := func(_ context.Context, actual analyzer.Config) (*analyzer.Index, error) {
		if actual.Root != config.Root || len(actual.BuildTags) != 1 || actual.BuildTags[0] != "integration" {
			return nil, fmt.Errorf("unexpected analyzer config: %#v", actual)
		}
		if calls.Add(1) == 1 {
			return fixtureIndex(), nil
		}
		close(started)
		<-release
		return replacement, nil
	}
	app := newTestApp(t, []ProjectConfig{{Name: DefaultProjectName, Analysis: config}}, engine.Options{Analyze: analyze})
	scanAll(t, app)

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
	if newSearch.Code != http.StatusOK || !strings.Contains(newSearch.Body.String(), "sample.Replacement") || calls.Load() != 2 {
		t.Fatalf("replacement index not installed: %d %s calls=%d", newSearch.Code, newSearch.Body.String(), calls.Load())
	}
}

func TestFailedRescanKeepsPreviousIndex(t *testing.T) {
	var calls atomic.Int32
	analyze := func(context.Context, analyzer.Config) (*analyzer.Index, error) {
		if calls.Add(1) == 1 {
			return fixtureIndex(), nil
		}
		return nil, fmt.Errorf("broken source")
	}
	app := newTestApp(t, []ProjectConfig{{Name: DefaultProjectName, Analysis: analyzer.Config{Root: "/work/project"}}}, engine.Options{Analyze: analyze})
	scanAll(t, app)

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

// TestSummaryEndpointMarksGeneratedIntent verifies the successful API envelope.
func TestSummaryEndpointMarksGeneratedIntent(t *testing.T) {
	cache, err := engine.NewSummaryCacheIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := newScannedApp(t, fixtureIndex(), engine.Options{Summarizer: engine.CommandSummarizer{Command: "printf \"{\\\"summary\\\":\\\"intent\\\"}\""}, SummaryCache: cache})
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/functions/root/summary", strings.NewReader("")))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var result protocol.SymbolSummaryResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Source != "generated" || result.Summary != "intent" {
		t.Fatalf("result = %#v, %v", result, err)
	}

	failing := newScannedApp(t, fixtureIndex(), engine.Options{Summarizer: engine.CommandSummarizer{Command: "false"}, SummaryCache: cache})
	failed := httptest.NewRecorder()
	failing.Handler().ServeHTTP(failed, httptest.NewRequest(http.MethodPost, "/api/functions/root/summary", nil))
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("provider failure status = %d body = %s", failed.Code, failed.Body.String())
	}
}

func TestProjectsScanLazilyAndKeepFailuresIsolated(t *testing.T) {
	configs := []ProjectConfig{
		{Name: "good", Analysis: analyzer.Config{Root: "/work/good"}},
		{Name: "bad", Analysis: analyzer.Config{Root: "/work/bad"}},
	}
	app := newTestApp(t, configs, engine.Options{Analyze: func(_ context.Context, config analyzer.Config) (*analyzer.Index, error) {
		if config.Root == "/work/bad" {
			return nil, fmt.Errorf("broken source")
		}
		return fixtureIndex(), nil
	}})

	projects := httptest.NewRecorder()
	app.Handler().ServeHTTP(projects, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), `"status":"unscanned"`) {
		t.Fatalf("initial projects = %d %s", projects.Code, projects.Body.String())
	}

	good := httptest.NewRecorder()
	app.Handler().ServeHTTP(good, httptest.NewRequest(http.MethodPost, "/api/projects/good/scan", nil))
	if good.Code != http.StatusOK || !strings.Contains(good.Body.String(), `"function_count":2`) {
		t.Fatalf("good scan = %d %s", good.Code, good.Body.String())
	}

	bad := httptest.NewRecorder()
	app.Handler().ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/projects/bad/scan", nil))
	if bad.Code != http.StatusInternalServerError || !strings.Contains(bad.Body.String(), "broken source") {
		t.Fatalf("bad scan = %d %s", bad.Code, bad.Body.String())
	}

	search := httptest.NewRecorder()
	app.Handler().ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/search?project=good&q=Root", nil))
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "sample.Root") {
		t.Fatalf("good project unavailable after bad scan: %d %s", search.Code, search.Body.String())
	}

	failedProjects := httptest.NewRecorder()
	app.Handler().ServeHTTP(failedProjects, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if !strings.Contains(failedProjects.Body.String(), `"status":"failed","error":"broken source"`) || !strings.Contains(failedProjects.Body.String(), `"function_count":2`) {
		t.Fatalf("projects after scans = %s", failedProjects.Body.String())
	}
}

func TestRescanReturnsLoadReportThroughEngine(t *testing.T) {
	index := fixtureIndex()
	index.LoadReport = analyzer.LoadReport{Root: "/work/project", Language: "go", BuildTags: []string{"integration"}, TotalPackageVariants: 3, FailedPackageVariants: 1, TotalUnits: 3, FailedUnits: 1, Diagnostics: []analyzer.LoadDiagnostic{
		{Kind: "type", Position: "broken/broken.go:3:9", Message: "undefined: missing", Packages: []string{"example.com/broken"}, Units: []string{"example.com/broken"}},
	}}
	app := newTestApp(t, []ProjectConfig{{Name: DefaultProjectName, Analysis: analyzer.Config{Root: "/work/project", BuildTags: []string{"integration"}}}}, engine.Options{Analyze: func(context.Context, analyzer.Config) (*analyzer.Index, error) { return index, nil }})

	result, err := app.Scan(testContext(t), "", "")
	if err != nil {
		t.Fatal(err)
	}
	report := result.LoadReport
	if !report.HasFailures() || report.FailedPackageVariants != 1 || len(report.Diagnostics) != 1 || report.Diagnostics[0].Position != "broken/broken.go:3" || report.Diagnostics[0].Packages[0] != "example.com/broken" {
		t.Fatalf("load report = %#v", report)
	}
	if !strings.Contains(report.String(), "go -C '/work/project' test -tags='integration' ./...") {
		t.Fatalf("rendered report = %s", report.String())
	}
	if result.GitStatus.Revision != "1234567890" || len(result.GitStatus.ChangedFunctions) != 1 || result.GitStatus.ChangedFunctions[0].File != "/work/sample.go" {
		t.Fatalf("Git status = %#v", result.GitStatus)
	}
}

func TestUnscannedAndUnknownResourcesReturnNotFound(t *testing.T) {
	app := newTestApp(t, []ProjectConfig{{Name: "only", Analysis: analyzer.Config{Root: "/work/project"}}}, engine.Options{})
	for _, path := range []string{"/api/search?q=x", "/api/search?project=missing", "/api/functions/root"} {
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d body = %s", path, response.Code, response.Body.String())
		}
	}
}

// newTestApp starts an in-process engine session and opens the projects.
func newTestApp(t *testing.T, configs []ProjectConfig, options engine.Options) *App {
	t.Helper()

	ctx := testContext(t)
	connection, err := engine.StartInProcess(ctx, options, protocol.PeerInfo{Name: "server-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := connection.Close(closeContext); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})

	app, err := New(ctx, connection.Client, configs)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return app
}

// newScannedApp serves one default project whose engine publishes index.
func newScannedApp(t *testing.T, index *analyzer.Index, options engine.Options) *App {
	t.Helper()

	options.Analyze = func(context.Context, analyzer.Config) (*analyzer.Index, error) { return index, nil }
	app := newTestApp(t, []ProjectConfig{{Name: DefaultProjectName, Analysis: analyzer.Config{Root: "/work/project"}}}, options)
	scanAll(t, app)
	return app
}

func scanAll(t *testing.T, app *App) {
	t.Helper()

	for _, name := range app.projectList {
		for _, language := range app.projects[name].list {
			if _, err := app.Scan(testContext(t), name, language); err != nil {
				t.Fatalf("Scan(%s, %s) = %v", name, language, err)
			}
		}
	}
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// fixtureIndex returns a minimal immutable graph for HTTP tests.
func fixtureIndex() *analyzer.Index {
	root := analyzer.Function{ID: "root", QualifiedName: "sample.Root", Package: "sample", File: "/work/sample.go", Line: 10, Public: true, Classification: analyzer.Classification{Kind: "pure"}, Change: &analyzer.FunctionChange{Kind: "updated", Diff: "--- a/sample.go\n+++ b/sample.go\n@@ -1 +1 @@\n-old\n+new\n"}}
	child := analyzer.Function{ID: "child", Name: "Root$1", QualifiedName: "sample.Root$1", Package: "sample", File: "/work/sample.go", Line: 12, Anonymous: true, Classification: analyzer.Classification{Kind: "unknown"}}
	edge := analyzer.Edge{CallerID: "root", CalleeID: "child", Kind: "call"}
	gitStatus := analyzer.GitSnapshot{Available: true, Branch: "main", Revision: "1234567890", ChangedFunctions: []analyzer.ChangedFunction{{ID: "root", QualifiedName: "sample.Root", Package: "sample", File: root.File, Line: root.Line, Kind: "updated", LeafDescendantCount: 2}}}
	return &analyzer.Index{Functions: map[string]analyzer.Function{"root": root, "child": child}, Edges: []analyzer.Edge{edge}, Outgoing: map[string][]analyzer.Edge{"root": {edge}}, Incoming: map[string][]analyzer.Edge{"child": {edge}}, Git: gitStatus}
}

func fixtureIndexWithRoot(id string, qualifiedName string) *analyzer.Index {
	root := analyzer.Function{ID: id, Name: qualifiedName, QualifiedName: qualifiedName, Package: "sample", Classification: analyzer.Classification{Kind: "pure"}}
	return &analyzer.Index{Functions: map[string]analyzer.Function{id: root}, Outgoing: map[string][]analyzer.Edge{}, Incoming: map[string][]analyzer.Edge{}}
}
