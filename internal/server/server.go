// Package server exposes analyzed projects through a local HTTP workbench.
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/telemetry"
)

//go:embed static/*
var staticFiles embed.FS

const legacyProjectName = "default"

// ProjectConfig identifies an independently analyzed Go module.
type ProjectConfig struct {
	Name     string
	Analysis analyzer.Config
}

// ProjectStatus describes a configured project's current scan state.
type ProjectStatus struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	FunctionCount int    `json:"function_count,omitempty"`
	Error         string `json:"error,omitempty"`
}

type project struct {
	config ProjectConfig
	index  atomic.Pointer[analyzer.Index]

	mu     sync.Mutex
	scan   sync.Mutex
	status string
	err    string
}

// App serves independently scanned projects and optional summaries.
type App struct {
	projects    map[string]*project
	projectList []string
	summarizer  Summarizer
	cache       *SummaryCache
	analyze     func(context.Context, analyzer.Config) (*analyzer.Index, error)
}

// RescanResult describes a newly installed analysis index.
type RescanResult struct {
	FunctionCount int                  `json:"function_count"`
	LoadReport    analyzer.LoadReport  `json:"load_report"`
	GitStatus     analyzer.GitSnapshot `json:"git_status"`
}

// New creates a fixed single-project workbench handler without network I/O.
func New(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache) (*App, error) {
	if index == nil {
		return nil, fmt.Errorf("create server: analysis index is required")
	}

	return newRegistry([]ProjectConfig{{Name: legacyProjectName}}, map[string]*analyzer.Index{legacyProjectName: index}, summarizer, cache, nil)
}

// NewRescannable creates a single-project workbench that can rebuild its index.
func NewRescannable(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache, config analyzer.Config) (*App, error) {
	if index == nil {
		return nil, fmt.Errorf("create server: analysis index is required")
	}

	return newRegistry([]ProjectConfig{{Name: legacyProjectName, Analysis: config}}, map[string]*analyzer.Index{legacyProjectName: index}, summarizer, cache, analyzer.Analyze)
}

// NewProjects creates a lazy multi-project workbench. Projects are analyzed on scan.
func NewProjects(configs []ProjectConfig, summarizer Summarizer, cache *SummaryCache) (*App, error) {
	return newRegistry(configs, nil, summarizer, cache, analyzer.Analyze)
}

// newApp remains the single-project test seam used by rescan coverage.
func newApp(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache, config analyzer.Config, analyze func(context.Context, analyzer.Config) (*analyzer.Index, error)) (*App, error) {
	return newRegistry([]ProjectConfig{{Name: legacyProjectName, Analysis: config}}, map[string]*analyzer.Index{legacyProjectName: index}, summarizer, cache, analyze)
}

func newRegistry(configs []ProjectConfig, indexes map[string]*analyzer.Index, summarizer Summarizer, cache *SummaryCache, analyze func(context.Context, analyzer.Config) (*analyzer.Index, error)) (*App, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("create server: at least one project is required")
	}
	if summarizer != nil && cache == nil {
		return nil, fmt.Errorf("create server: summary cache is required when a summarizer is configured")
	}

	app := &App{projects: make(map[string]*project, len(configs)), summarizer: summarizer, cache: cache, analyze: analyze}
	for _, config := range configs {
		name := strings.TrimSpace(config.Name)
		if name == "" || app.projects[name] != nil {
			return nil, fmt.Errorf("create server: project names must be unique and non-empty")
		}
		entry := &project{config: config, status: "unscanned"}
		if index := indexes[name]; index != nil {
			entry.index.Store(index)
			entry.status = "ready"
		}
		app.projects[name] = entry
		app.projectList = append(app.projectList, name)
	}
	return app, nil
}

// Handler returns the complete local HTTP API and embedded UI.
func (app *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/projects", app.handleProjects)
	mux.HandleFunc("POST /api/projects/{name}/scan", app.handleProjectScan)
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

func (app *App) handleProjects(response http.ResponseWriter, _ *http.Request) {
	result := make([]ProjectStatus, 0, len(app.projectList))
	for _, name := range app.projectList {
		result = append(result, app.projects[name].snapshot())
	}
	writeJSON(response, http.StatusOK, result)
}

func (app *App) handleProjectScan(response http.ResponseWriter, request *http.Request) {
	app.scan(response, request, request.PathValue("name"))
}
func (app *App) handleRescan(response http.ResponseWriter, request *http.Request) {
	app.scan(response, request, request.URL.Query().Get("project"))
}

func (app *App) scan(response http.ResponseWriter, request *http.Request, name string) {
	if strings.TrimSpace(name) == "" && len(app.projectList) == 1 {
		name = app.projectList[0]
	}
	entry := app.projects[name]
	if entry == nil {
		writeError(response, http.StatusNotFound, fmt.Errorf("project not found"))
		return
	}
	if app.analyze == nil {
		writeError(response, http.StatusNotImplemented, fmt.Errorf("codebase rescanning is not configured"))
		return
	}
	if !entry.scan.TryLock() {
		writeError(response, http.StatusConflict, fmt.Errorf("a codebase scan is already in progress"))
		return
	}
	defer entry.scan.Unlock()

	entry.mu.Lock()
	entry.status, entry.err = "loading", ""
	entry.mu.Unlock()

	index, err := app.analyze(request.Context(), entry.config.Analysis)
	if err != nil {
		entry.mu.Lock()
		entry.status, entry.err = "failed", err.Error()
		entry.mu.Unlock()
		writeError(response, http.StatusInternalServerError, fmt.Errorf("scan project: %w", err))
		return
	}
	entry.index.Store(index)
	entry.mu.Lock()
	entry.status = "ready"
	entry.mu.Unlock()
	writeJSON(response, http.StatusOK, RescanResult{FunctionCount: len(index.Functions), LoadReport: index.LoadReport, GitStatus: index.Git})
}

func (app *App) project(name string) (*project, error) {
	if strings.TrimSpace(name) == "" && len(app.projectList) == 1 {
		name = app.projectList[0]
	}
	entry := app.projects[name]
	if entry == nil {
		return nil, fmt.Errorf("project not found")
	}
	if entry.index.Load() == nil {
		return nil, fmt.Errorf("project %q has not been scanned", name)
	}
	return entry, nil
}

func (entry *project) snapshot() ProjectStatus {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	result := ProjectStatus{Name: entry.config.Name, Status: entry.status, Error: entry.err}
	if index := entry.index.Load(); index != nil {
		result.FunctionCount = len(index.Functions)
	}
	return result
}

func (app *App) handleGitStatus(response http.ResponseWriter, request *http.Request) {
	entry, err := app.project(request.URL.Query().Get("project"))
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	writeJSON(response, http.StatusOK, entry.index.Load().Git)
}
func (app *App) handleSearch(response http.ResponseWriter, request *http.Request) {
	entry, err := app.project(request.URL.Query().Get("project"))
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	writeJSON(response, http.StatusOK, entry.index.Load().Search(request.URL.Query().Get("q"), request.URL.Query().Get("tests") == "true", 100))
}
func (app *App) handleGraph(response http.ResponseWriter, request *http.Request) {
	entry, err := app.project(request.URL.Query().Get("project"))
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	depth, _ := strconv.Atoi(request.URL.Query().Get("depth"))
	graph, err := entry.index.Load().Focus(request.URL.Query().Get("root"), request.URL.Query().Get("direction"), depth, request.URL.Query().Get("tests") == "true")
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	writeJSON(response, http.StatusOK, graph)
}
func (app *App) handleFunction(response http.ResponseWriter, request *http.Request) {
	entry, err := app.project(request.URL.Query().Get("project"))
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	function, exists := entry.index.Load().Function(request.PathValue("id"))
	if !exists {
		writeError(response, http.StatusNotFound, fmt.Errorf("function not found"))
		return
	}
	writeJSON(response, http.StatusOK, function)
}
func (app *App) handleSummary(response http.ResponseWriter, request *http.Request) {
	if app.summarizer == nil {
		writeError(response, http.StatusNotImplemented, fmt.Errorf("AI summarization is disabled; configure --summarizer-command"))
		return
	}
	entry, err := app.project(request.URL.Query().Get("project"))
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}
	function, exists := entry.index.Load().Function(request.PathValue("id"))
	if !exists {
		writeError(response, http.StatusNotFound, fmt.Errorf("function not found"))
		return
	}
	summaryRequest := SummaryRequest{QualifiedName: function.QualifiedName, Signature: function.Signature, Source: function.Source, Documentation: function.Intent, Contracts: function.Contracts}
	if summary, cached := app.cache.Get(app.summarizer.Identity(), summaryRequest); cached {
		writeJSON(response, http.StatusOK, SummaryResult{Summary: summary, Source: "generated", Cached: true})
		return
	}
	summary, err := app.summarizer.Summarize(request.Context(), summaryRequest)
	if err != nil {
		writeError(response, http.StatusBadGateway, err)
		return
	}
	if err := app.cache.Put(app.summarizer.Identity(), summaryRequest, summary); err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, SummaryResult{Summary: summary, Source: "generated"})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": strings.TrimSpace(err.Error())})
}
