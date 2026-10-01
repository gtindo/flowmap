// Package server adapts the Flowmap engine protocol to a local HTTP API and
// serves the embedded browser workbench. It is a protocol client: every
// analysis and query goes through an engine session.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
	"github.com/gtindo/flowmap/internal/telemetry"
)

//go:embed static/*
var staticFiles embed.FS

// DefaultProjectName names the single project served from a module path.
const DefaultProjectName = "default"

// searchResultLimit bounds the browser search list.
const searchResultLimit = 100

// Browser-facing language status values.
const (
	statusUnscanned = "unscanned"
	statusLoading   = "loading"
	statusReady     = "ready"
	statusFailed    = "failed"
)

var (
	errNotFound        = errors.New("not found")
	errScanInProgress  = errors.New("a codebase scan is already in progress")
	errSummaryDisabled = errors.New("AI summarization is disabled; configure --summarizer-command")
)

// ProjectConfig identifies one independently analyzed repository.
type ProjectConfig struct {
	Name     string
	Analysis analyzer.Config // Deprecated single-language shorthand.
	Analyses []analyzer.Config
}

// ProjectStatus describes a configured project's current scan state.
type ProjectStatus struct {
	Name          string           `json:"name"`
	Status        string           `json:"status"`
	FunctionCount int              `json:"function_count,omitempty"`
	Error         string           `json:"error,omitempty"`
	Languages     []LanguageStatus `json:"languages,omitempty"`
}

// LanguageStatus describes one language view within a configured project.
type LanguageStatus struct {
	Language      string `json:"language"`
	Status        string `json:"status"`
	FunctionCount int    `json:"function_count,omitempty"`
	Error         string `json:"error,omitempty"`
}

// RescanResult describes a newly published analysis snapshot.
type RescanResult struct {
	FunctionCount int                  `json:"function_count"`
	LoadReport    analyzer.LoadReport  `json:"load_report"`
	GitStatus     analyzer.GitSnapshot `json:"git_status"`
}

type project struct {
	name        string
	root        string
	workspaceID string
	languages   map[string]*languageProject
	list        []string
}

// languageProject identifies one engine language view. View state is never
// cached here; it is read from the engine with workspace/get.
type languageProject struct {
	language string
	viewID   string
}

// analysisOutcome is the terminal notification for one analysis.
type analysisOutcome struct {
	published *protocol.Snapshot
	failure   *protocol.Failure
}

// App serves independently scanned projects through an engine client.
type App struct {
	client      *protocol.Client
	summaries   bool
	projects    map[string]*project
	projectList []string

	mu        sync.Mutex
	waiters   map[string]chan analysisOutcome
	finished  map[string]analysisOutcome
	abandoned map[string]bool
}

// New opens one engine workspace per project. The client must already be
// initialized; views start unscanned until Scan or a scan endpoint runs.
// Side Effect (Edge): sends workspace/open requests to the engine.
func New(ctx context.Context, client *protocol.Client, configs []ProjectConfig) (*App, error) {
	if client == nil {
		return nil, fmt.Errorf("create server: engine client is required")
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("create server: at least one project is required")
	}

	app := &App{
		client:    client,
		summaries: client.Session().Capabilities.SymbolSummary,
		projects:  make(map[string]*project, len(configs)),
		waiters:   make(map[string]chan analysisOutcome),
		finished:  make(map[string]analysisOutcome),
		abandoned: make(map[string]bool),
	}
	client.OnNotification(app.handleNotification)

	for _, config := range configs {
		entry, err := app.openProject(ctx, config)
		if err != nil {
			return nil, err
		}
		app.projects[entry.name] = entry
		app.projectList = append(app.projectList, entry.name)
	}
	return app, nil
}

func (app *App) openProject(ctx context.Context, config ProjectConfig) (*project, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" || app.projects[name] != nil {
		return nil, fmt.Errorf("create server: project names must be unique and non-empty")
	}

	analyses := config.Analyses
	if len(analyses) == 0 {
		analyses = []analyzer.Config{config.Analysis}
	}

	root := analyses[0].Root
	views := make([]protocol.ViewSpec, 0, len(analyses))
	seen := make(map[string]bool, len(analyses))
	for _, analysis := range analyses {
		language := configLanguage(analysis)
		if seen[language] {
			return nil, fmt.Errorf("create server: project %q contains duplicate language %q", name, language)
		}
		if analysis.Root != root {
			return nil, fmt.Errorf("create server: project %q language views must share one root", name)
		}

		seen[language] = true
		views = append(views, protocol.ViewSpec{Language: language, BuildTags: analysis.BuildTags})
	}

	opened, err := app.client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: protocol.FileURI(root), Name: name, Views: views})
	if err != nil {
		return nil, fmt.Errorf("open project %q: %w", name, err)
	}

	entry := &project{name: name, root: root, workspaceID: opened.WorkspaceID, languages: make(map[string]*languageProject, len(opened.Views))}
	for _, openedView := range opened.Views {
		entry.languages[openedView.Language] = &languageProject{language: openedView.Language, viewID: openedView.ViewID}
		entry.list = append(entry.list, openedView.Language)
	}
	sort.Strings(entry.list)
	return entry, nil
}

// Handler returns the complete local HTTP API and embedded UI.
func (app *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", app.handleProjects)
	mux.HandleFunc("POST /api/projects/{name}/scan", app.handleProjectScan)
	mux.HandleFunc("POST /api/projects/{name}/languages/{language}/scan", app.handleLanguageScan)
	mux.HandleFunc("GET /api/search", app.handleSearch)
	mux.HandleFunc("GET /api/graph", app.handleGraph)
	mux.HandleFunc("GET /api/functions/{id}", app.handleFunction)
	mux.HandleFunc("GET /api/git-status", app.handleGitStatus)
	mux.HandleFunc("POST /api/functions/{id}/summary", app.handleSummary)
	mux.HandleFunc("POST /api/rescan", app.handleRescan)

	assets, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(assets))
	mux.Handle("/", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/manifest.webmanifest" {
			response.Header().Set("Content-Type", "application/manifest+json")
		}
		fileServer.ServeHTTP(response, request)
	}))
	return otelhttp.NewHandler(logRequests(mux), "flowmap.http")
}

// Listen starts the imperative HTTP edge and shuts it down with ctx.
func (app *App) Listen(ctx context.Context, address string) error {
	server := &http.Server{Addr: address, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.ListenAndServe() }()
	select {
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve Flowmap: %w", err)
	case <-ctx.Done():
		slog.InfoContext(ctx, "flowmap server shutting down")
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down Flowmap: %w", err)
		}
		return nil
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !telemetry.Enabled() {
			next.ServeHTTP(response, request)
			return
		}
		start := time.Now()
		recorder := statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(&recorder, request)
		slog.InfoContext(request.Context(), "http request handled", "method", request.Method, "path", request.URL.Path, "status", recorder.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (app *App) handleProjects(response http.ResponseWriter, request *http.Request) {
	result := make([]ProjectStatus, 0, len(app.projectList))
	for _, name := range app.projectList {
		status, err := app.projectStatus(request.Context(), app.projects[name])
		if err != nil {
			writeError(response, httpStatus(err), err)
			return
		}
		result = append(result, status)
	}
	writeJSON(response, http.StatusOK, result)
}

func (app *App) handleProjectScan(response http.ResponseWriter, request *http.Request) {
	app.writeScan(response, request, request.PathValue("name"), request.URL.Query().Get("language"))
}

func (app *App) handleLanguageScan(response http.ResponseWriter, request *http.Request) {
	app.writeScan(response, request, request.PathValue("name"), request.PathValue("language"))
}

func (app *App) handleRescan(response http.ResponseWriter, request *http.Request) {
	app.writeScan(response, request, request.URL.Query().Get("project"), request.URL.Query().Get("language"))
}

func (app *App) writeScan(response http.ResponseWriter, request *http.Request, name string, language string) {
	result, err := app.Scan(request.Context(), name, language)
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

// Scan asks the engine to analyze one language view, waits for publication,
// and returns the published snapshot's summary. A failed scan leaves the
// previous snapshot queryable.
// Side Effect (Edge): runs engine analysis and queries through the protocol.
func (app *App) Scan(ctx context.Context, name string, language string) (RescanResult, error) {
	entry, languageEntry, err := app.languageEntry(name, language)
	if err != nil {
		return RescanResult{}, err
	}

	analysisID, err := app.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: languageEntry.viewID})
	if err != nil {
		if hasCode(err, protocol.CodeAnalysisAlreadyRunning) {
			return RescanResult{}, errScanInProgress
		}
		return RescanResult{}, fmt.Errorf("scan project: %w", err)
	}

	outcome, err := app.awaitAnalysis(ctx, analysisID)
	if err != nil {
		return RescanResult{}, fmt.Errorf("scan project: %w", err)
	}
	if outcome.failure != nil {
		return RescanResult{}, fmt.Errorf("scan project: %s", outcome.failure.Message)
	}

	query := protocol.SnapshotQuery{ViewID: languageEntry.viewID, SnapshotID: outcome.published.SnapshotID}
	report, err := app.loadReport(ctx, query, entry.root)
	if err != nil {
		return RescanResult{}, fmt.Errorf("scan project: %w", err)
	}
	gitStatus, err := app.gitStatus(ctx, query)
	if err != nil {
		return RescanResult{}, fmt.Errorf("scan project: %w", err)
	}

	return RescanResult{FunctionCount: outcome.published.SymbolCount, LoadReport: report, GitStatus: gitStatus}, nil
}

// awaitAnalysis waits for the terminal notification of one analysis and
// requests cancellation if ctx ends first.
func (app *App) awaitAnalysis(ctx context.Context, analysisID string) (analysisOutcome, error) {
	app.mu.Lock()
	if outcome, done := app.finished[analysisID]; done {
		delete(app.finished, analysisID)
		app.mu.Unlock()
		return outcome, nil
	}
	outcomes := make(chan analysisOutcome, 1)
	app.waiters[analysisID] = outcomes
	app.mu.Unlock()

	select {
	case outcome := <-outcomes:
		return outcome, nil
	case <-app.client.Done():
		return analysisOutcome{}, fmt.Errorf("engine connection closed")
	case <-ctx.Done():
		app.mu.Lock()
		delete(app.waiters, analysisID)
		app.abandoned[analysisID] = true
		app.mu.Unlock()

		_, _ = app.client.CancelAnalysis(context.WithoutCancel(ctx), analysisID)
		return analysisOutcome{}, ctx.Err()
	}
}

// handleNotification wakes scan waiters. It runs on the client read loop, so
// it only updates local state.
func (app *App) handleNotification(method string, params json.RawMessage) {
	switch method {
	case protocol.MethodAnalysisPublished:
		var published protocol.AnalysisPublished
		if json.Unmarshal(params, &published) != nil {
			return
		}
		app.recordOutcome(published.AnalysisID, analysisOutcome{published: &published.Snapshot})
	case protocol.MethodAnalysisFailed:
		var failed protocol.AnalysisFailure
		if json.Unmarshal(params, &failed) != nil {
			return
		}
		app.recordOutcome(failed.AnalysisID, analysisOutcome{failure: &failed.Failure})
	}
}

func (app *App) recordOutcome(analysisID string, outcome analysisOutcome) {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.abandoned[analysisID] {
		delete(app.abandoned, analysisID)
		return
	}
	if outcomes := app.waiters[analysisID]; outcomes != nil {
		delete(app.waiters, analysisID)
		outcomes <- outcome
		return
	}
	app.finished[analysisID] = outcome
}

// language resolves a project view to the engine's current snapshot.
func (app *App) language(ctx context.Context, name string, language string) (*project, protocol.SnapshotQuery, error) {
	entry, languageEntry, err := app.languageEntry(name, language)
	if err != nil {
		return nil, protocol.SnapshotQuery{}, err
	}

	states, err := app.viewStates(ctx, entry)
	if err != nil {
		return nil, protocol.SnapshotQuery{}, err
	}

	current := states[languageEntry.viewID].CurrentSnapshot
	if current == nil {
		return nil, protocol.SnapshotQuery{}, fmt.Errorf("project %q language %q has not been scanned: %w", entry.name, languageEntry.language, errNotFound)
	}
	return entry, protocol.SnapshotQuery{ViewID: languageEntry.viewID, SnapshotID: current.SnapshotID}, nil
}

// viewStates reads every view's load state from the engine.
func (app *App) viewStates(ctx context.Context, entry *project) (map[string]protocol.LoadState, error) {
	opened, err := app.client.GetWorkspace(ctx, entry.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("read project %q state: %w", entry.name, err)
	}

	states := make(map[string]protocol.LoadState, len(opened.Views))
	for _, view := range opened.Views {
		states[view.ViewID] = view.LoadState
	}
	return states, nil
}

func (app *App) languageEntry(name string, language string) (*project, *languageProject, error) {
	if strings.TrimSpace(name) == "" && len(app.projectList) == 1 {
		name = app.projectList[0]
	}
	entry := app.projects[name]
	if entry == nil {
		return nil, nil, fmt.Errorf("project %w", errNotFound)
	}
	if strings.TrimSpace(language) == "" && len(entry.list) == 1 {
		language = entry.list[0]
	}
	languageEntry := entry.languages[language]
	if languageEntry == nil {
		return nil, nil, fmt.Errorf("language %w", errNotFound)
	}
	return entry, languageEntry, nil
}

func configLanguage(config analyzer.Config) string {
	if strings.TrimSpace(config.Language) == "" {
		return analyzer.LanguageGo
	}
	return strings.ToLower(strings.TrimSpace(config.Language))
}

func (app *App) projectStatus(ctx context.Context, entry *project) (ProjectStatus, error) {
	states, err := app.viewStates(ctx, entry)
	if err != nil {
		return ProjectStatus{}, err
	}

	result := ProjectStatus{Name: entry.name, Status: statusReady}
	for _, language := range entry.list {
		status := languageStatus(language, states[entry.languages[language].viewID])
		result.Languages = append(result.Languages, status)
		if status.Status != statusReady {
			result.Status = status.Status
			result.Error = status.Error
		}
		result.FunctionCount += status.FunctionCount
	}
	return result, nil
}

// languageStatus maps an engine load state onto the browser's status model.
// Operations (Pure): data mapping only.
func languageStatus(language string, state protocol.LoadState) LanguageStatus {
	status := LanguageStatus{Language: language, Status: statusUnscanned}
	switch state.State {
	case protocol.LoadStateAnalyzing:
		status.Status = statusLoading
	case protocol.LoadStateReady:
		status.Status = statusReady
	case protocol.LoadStateFailed:
		status.Status = statusFailed
	}

	if state.CurrentSnapshot != nil {
		status.FunctionCount = state.CurrentSnapshot.SymbolCount
	}
	if state.Failure != nil {
		status.Error = state.Failure.Message
	}
	return status
}

func (app *App) handleGitStatus(response http.ResponseWriter, request *http.Request) {
	_, query, err := app.language(request.Context(), request.URL.Query().Get("project"), request.URL.Query().Get("language"))
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}

	gitStatus, err := app.gitStatus(request.Context(), query)
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}
	writeJSON(response, http.StatusOK, gitStatus)
}

func (app *App) handleSearch(response http.ResponseWriter, request *http.Request) {
	_, query, err := app.language(request.Context(), request.URL.Query().Get("project"), request.URL.Query().Get("language"))
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}

	page, err := app.client.SearchSymbols(request.Context(), protocol.SymbolSearchParams{
		SnapshotQuery: query,
		Query:         request.URL.Query().Get("q"),
		IncludeTests:  request.URL.Query().Get("tests") == "true",
		Page:          &protocol.Page{Limit: searchResultLimit},
	})
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}
	writeJSON(response, http.StatusOK, searchResults(page.Items))
}

func (app *App) handleGraph(response http.ResponseWriter, request *http.Request) {
	entry, query, err := app.language(request.Context(), request.URL.Query().Get("project"), request.URL.Query().Get("language"))
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}

	// The HTTP API historically coerced traversal options; the protocol rejects them.
	depth, _ := strconv.Atoi(request.URL.Query().Get("depth"))
	depth = min(max(depth, 0), protocol.MaxGraphDepth)
	direction := request.URL.Query().Get("direction")
	if direction != protocol.DirectionUpstream && direction != protocol.DirectionBoth {
		direction = protocol.DirectionDownstream
	}

	neighborhood, err := app.client.Neighborhood(request.Context(), protocol.GraphNeighborhoodParams{
		SnapshotQuery: query,
		RootSymbolID:  request.URL.Query().Get("root"),
		Direction:     direction,
		Depth:         &depth,
		IncludeTests:  request.URL.Query().Get("tests") == "true",
	})
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}
	writeJSON(response, http.StatusOK, graphModel(neighborhood, entry.root))
}

func (app *App) handleFunction(response http.ResponseWriter, request *http.Request) {
	_, query, err := app.language(request.Context(), request.URL.Query().Get("project"), request.URL.Query().Get("language"))
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}

	symbol, err := app.client.GetSymbol(request.Context(), protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: request.PathValue("id")})
	if err != nil {
		if hasCode(err, protocol.CodeSymbolNotFound) {
			writeError(response, http.StatusNotFound, fmt.Errorf("function not found"))
			return
		}
		writeError(response, httpStatus(err), err)
		return
	}
	writeJSON(response, http.StatusOK, functionModel(symbol))
}

func (app *App) handleSummary(response http.ResponseWriter, request *http.Request) {
	if !app.summaries {
		writeError(response, http.StatusNotImplemented, errSummaryDisabled)
		return
	}
	_, query, err := app.language(request.Context(), request.URL.Query().Get("project"), request.URL.Query().Get("language"))
	if err != nil {
		writeError(response, httpStatus(err), err)
		return
	}

	result, err := app.client.SummarizeSymbol(request.Context(), protocol.SymbolSummaryParams{SnapshotQuery: query, SymbolID: request.PathValue("id")})
	if err != nil {
		switch {
		case hasCode(err, protocol.CodeSymbolNotFound):
			writeError(response, http.StatusNotFound, fmt.Errorf("function not found"))
		case hasCode(err, protocol.CodeInternalError):
			// Internal summary failures come from the configured provider command.
			writeError(response, http.StatusBadGateway, err)
		default:
			writeError(response, httpStatus(err), err)
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

// gitStatus collects every page of captured Git changes.
func (app *App) gitStatus(ctx context.Context, query protocol.SnapshotQuery) (analyzer.GitSnapshot, error) {
	params := protocol.ChangesListParams{SnapshotQuery: query, Page: &protocol.Page{Limit: protocol.MaxPageLimit}}
	var state protocol.GitState
	changes := make([]protocol.GitChange, 0)
	for {
		page, err := app.client.ListChanges(ctx, params)
		if err != nil {
			return analyzer.GitSnapshot{}, err
		}

		state = page.GitState
		changes = append(changes, page.Items...)
		if page.NextCursor == "" {
			return gitSnapshotModel(state, changes), nil
		}
		params.Page.Cursor = page.NextCursor
	}
}

// loadReport collects every page of load diagnostics.
func (app *App) loadReport(ctx context.Context, query protocol.SnapshotQuery, root string) (analyzer.LoadReport, error) {
	params := protocol.DiagnosticsListParams{SnapshotQuery: query, Page: &protocol.Page{Limit: protocol.MaxPageLimit}}
	var report protocol.LoadReport
	items := make([]protocol.Diagnostic, 0)
	for {
		page, err := app.client.ListDiagnostics(ctx, params)
		if err != nil {
			return analyzer.LoadReport{}, err
		}

		report = page.LoadReport
		items = append(items, page.Items...)
		if page.NextCursor == "" {
			return loadReportModel(report, items, root), nil
		}
		params.Page.Cursor = page.NextCursor
	}
}

func hasCode(err error, code int) bool {
	var rpcError *protocol.Error
	return errors.As(err, &rpcError) && rpcError.Code == code
}

// httpStatus maps adapter and protocol errors onto HTTP status codes.
func httpStatus(err error) int {
	var rpcError *protocol.Error
	switch {
	case errors.Is(err, errNotFound):
		return http.StatusNotFound
	case errors.Is(err, errScanInProgress):
		return http.StatusConflict
	case errors.Is(err, context.Canceled):
		return http.StatusServiceUnavailable
	case !errors.As(err, &rpcError):
		return http.StatusInternalServerError
	}

	switch rpcError.Code {
	case protocol.CodeInvalidParams:
		return http.StatusBadRequest
	case protocol.CodeSymbolNotFound, protocol.CodeViewNotFound, protocol.CodeWorkspaceNotFound, protocol.CodeSnapshotUnavailable:
		return http.StatusNotFound
	case protocol.CodeAnalysisAlreadyRunning:
		return http.StatusConflict
	case protocol.CodeCapabilityNotSupported:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": strings.TrimSpace(err.Error())})
}
