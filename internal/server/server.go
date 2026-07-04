// Package server exposes an analyzed index through a local HTTP workbench.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gtindo/flowmap/internal/analyzer"
)

//go:embed static/*
var staticFiles embed.FS

// App serves one immutable analysis index and optional summary provider.
type App struct {
	index      atomic.Pointer[analyzer.Index]
	summarizer Summarizer
	cache      *SummaryCache
	analysis   analyzer.Config
	analyze    func(context.Context, analyzer.Config) (*analyzer.Index, error)
	rescan     sync.Mutex
}

// RescanResult describes a newly installed analysis index.
type RescanResult struct {
	FunctionCount int                  `json:"function_count"`
	LoadReport    analyzer.LoadReport  `json:"load_report"`
	GitStatus     analyzer.GitSnapshot `json:"git_status"`
}

// New creates a local workbench handler without starting network I/O.
func New(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache) (*App, error) {
	return newApp(index, summarizer, cache, analyzer.Config{}, nil)
}

// NewRescannable creates a workbench that can rebuild its index in process.
func NewRescannable(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache, config analyzer.Config) (*App, error) {
	return newApp(index, summarizer, cache, config, analyzer.Analyze)
}

// newApp validates and assembles the shared server state for fixed and rescannable apps.
func newApp(index *analyzer.Index, summarizer Summarizer, cache *SummaryCache, config analyzer.Config, analyze func(context.Context, analyzer.Config) (*analyzer.Index, error)) (*App, error) {
	if index == nil {
		return nil, fmt.Errorf("create server: analysis index is required")
	}

	if summarizer != nil && cache == nil {
		return nil, fmt.Errorf("create server: summary cache is required when a summarizer is configured")
	}

	app := &App{summarizer: summarizer, cache: cache, analysis: config, analyze: analyze}
	app.index.Store(index)

	return app, nil
}

// Handler returns the complete local HTTP API and embedded UI.
func (app *App) Handler() http.Handler {
	mux := http.NewServeMux()
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

	return mux
}

// handleGitStatus returns the repository state captured by the active scan.
func (app *App) handleGitStatus(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, app.index.Load().Git)
}

// Listen starts the imperative HTTP edge and shuts it down with ctx.
func (app *App) Listen(ctx context.Context, address string) error {
	server := &http.Server{Addr: address, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	errorChannel := make(chan error, 1)

	go func() { errorChannel <- server.ListenAndServe() }()

	select {
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve Flowmap: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down Flowmap: %w", err)
		}

		return nil
	}
}

// handleSearch returns compact symbol matches.
func (app *App) handleSearch(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, app.index.Load().Search(request.URL.Query().Get("q"), request.URL.Query().Get("tests") == "true", 100))
}

// handleGraph returns a bounded function neighborhood.
func (app *App) handleGraph(response http.ResponseWriter, request *http.Request) {
	depth, _ := strconv.Atoi(request.URL.Query().Get("depth"))
	graph, err := app.index.Load().Focus(request.URL.Query().Get("root"), request.URL.Query().Get("direction"), depth, request.URL.Query().Get("tests") == "true")
	if err != nil {
		writeError(response, http.StatusNotFound, err)
		return
	}

	writeJSON(response, http.StatusOK, graph)
}

// handleFunction returns source and detailed contract information for one node.
func (app *App) handleFunction(response http.ResponseWriter, request *http.Request) {
	function, exists := app.index.Load().Function(request.PathValue("id"))
	if !exists {
		writeError(response, http.StatusNotFound, fmt.Errorf("function not found"))
		return
	}

	writeJSON(response, http.StatusOK, function)
}

// handleSummary generates missing intent only after an explicit browser action.
func (app *App) handleSummary(response http.ResponseWriter, request *http.Request) {
	if app.summarizer == nil {
		writeError(response, http.StatusNotImplemented, fmt.Errorf("AI summarization is disabled; configure --summarizer-command"))
		return
	}

	function, exists := app.index.Load().Function(request.PathValue("id"))
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

// handleRescan builds a replacement beside the current immutable index, then
// installs it in one atomic operation so readers never observe partial state.
func (app *App) handleRescan(response http.ResponseWriter, request *http.Request) {
	if app.analyze == nil {
		writeError(response, http.StatusNotImplemented, fmt.Errorf("codebase rescanning is not configured"))
		return
	}

	if !app.rescan.TryLock() {
		writeError(response, http.StatusConflict, fmt.Errorf("a codebase rescan is already in progress"))
		return
	}

	defer app.rescan.Unlock()

	index, err := app.analyze(request.Context(), app.analysis)
	if err != nil {
		writeError(response, http.StatusInternalServerError, fmt.Errorf("rescan codebase: %w", err))
		return
	}

	app.index.Store(index)

	writeJSON(response, http.StatusOK, RescanResult{FunctionCount: len(index.Functions), LoadReport: index.LoadReport, GitStatus: index.Git})
}

// writeJSON sends one JSON response with a stable content type.
func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

// writeError sends a small JSON error envelope.
func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": strings.TrimSpace(err.Error())})
}
