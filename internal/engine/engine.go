// Package engine implements the Flowmap engine protocol session: workspace and
// language-view registry, analysis scheduling, immutable snapshot retention,
// and snapshot-addressed queries over analyzer indexes.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
)

// retainedSnapshotsPerView keeps the current snapshot plus its predecessor so
// queries issued just before a publication can still complete.
const retainedSnapshotsPerView = 2

// engineName identifies this implementation during initialization.
const engineName = "flowmap"

// AnalyzeFunc builds one complete analyzer index for a language view.
type AnalyzeFunc func(context.Context, analyzer.Config) (*analyzer.Index, error)

// Options configures one engine session.
type Options struct {
	// Version is reported in engineInfo.
	Version string
	// Analyze defaults to analyzer.Analyze; tests substitute deterministic fakes.
	Analyze AnalyzeFunc
	// Summarizer enables symbol/summary when set together with SummaryCache.
	Summarizer   Summarizer
	SummaryCache *SummaryCache
	// Logger receives operator diagnostics; protocol output never goes here.
	Logger *slog.Logger
}

type sessionState int

const (
	sessionUninitialized sessionState = iota
	sessionInitialized
	sessionShuttingDown
	sessionShutdown
)

// Engine is one protocol session. Create it with New and run it with Serve.
type Engine struct {
	options Options
	logger  *slog.Logger

	// ctx bounds analyses and summaries for the life of the session.
	ctx    context.Context
	cancel context.CancelFunc

	writer *protocol.FrameWriter

	mu         sync.Mutex
	state      sessionState
	closed     bool
	nextID     int
	workspaces map[string]*workspace
	views      map[string]*view
	analyses   map[string]*analysis

	handlers sync.WaitGroup
}

type workspace struct {
	id      string
	rootURI string
	root    string
	name    string
	views   []*view
}

type view struct {
	id        string
	workspace *workspace
	language  string
	config    analyzer.Config

	// emit orders this view's notifications; held from commit until written.
	emit sync.Mutex

	// The remaining fields are guarded by Engine.mu.
	closed      bool
	active      *analysis
	lastFailure *protocol.Failure
	revision    int
	snapshots   []*snapshot
}

type snapshot struct {
	descriptor protocol.Snapshot
	index      *analyzer.Index
}

type analysis struct {
	id       string
	view     *view
	ctx      context.Context
	cancel   context.CancelFunc
	terminal bool
}

// New creates an uninitialized engine session.
func New(options Options) *Engine {
	if options.Analyze == nil {
		options.Analyze = analyzer.Analyze
	}

	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Engine{
		options:    options,
		logger:     logger,
		workspaces: make(map[string]*workspace),
		views:      make(map[string]*view),
		analyses:   make(map[string]*analysis),
	}
}

func (engine *Engine) capabilities() protocol.Capabilities {
	return protocol.Capabilities{
		Languages:     []string{analyzer.LanguageGo, analyzer.LanguageJavaScript},
		SymbolSummary: engine.options.Summarizer != nil && engine.options.SummaryCache != nil,
	}
}

// identifier issues a process-unique opaque identifier. Callers hold mu.
func (engine *Engine) identifier(prefix string) string {
	engine.nextID++
	return prefix + "-" + strconv.Itoa(engine.nextID)
}

func (engine *Engine) initialize(params protocol.InitializeParams) (protocol.InitializeResult, *protocol.Error) {
	if strings.TrimSpace(params.ClientInfo.Name) == "" {
		return protocol.InitializeResult{}, protocol.Errorf(protocol.CodeInvalidParams, "clientInfo.name is required")
	}
	if params.ProtocolVersion != protocol.Version {
		return protocol.InitializeResult{}, protocol.NewError(protocol.CodeProtocolVersionMismatch, "unsupported protocol version", map[string]any{"supported": []string{protocol.Version}})
	}

	capabilities := engine.capabilities()
	missing := make([]string, 0)
	for _, required := range params.RequiredCapabilities {
		supported := required == protocol.CapabilityLanguages || (required == protocol.CapabilitySymbolSummary && capabilities.SymbolSummary)
		if !supported {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		return protocol.InitializeResult{}, protocol.NewError(protocol.CodeCapabilityNotSupported, "required capabilities are not supported", map[string]any{"capabilities": missing})
	}

	engine.mu.Lock()
	engine.state = sessionInitialized
	engine.mu.Unlock()

	return protocol.InitializeResult{
		ProtocolVersion: protocol.Version,
		EngineInfo:      protocol.PeerInfo{Name: engineName, Version: engine.options.Version},
		Capabilities:    capabilities,
	}, nil
}

func (engine *Engine) openWorkspace(params protocol.WorkspaceOpenParams) (protocol.Workspace, *protocol.Error) {
	root, err := protocol.PathFromURI(params.RootURI)
	if err != nil {
		return protocol.Workspace{}, protocol.Errorf(protocol.CodeInvalidParams, "rootUri: %v", err)
	}
	if len(params.Views) == 0 {
		return protocol.Workspace{}, protocol.Errorf(protocol.CodeInvalidParams, "views must contain at least one language view")
	}

	configs := make([]analyzer.Config, 0, len(params.Views))
	seen := make(map[string]bool, len(params.Views))
	for _, spec := range params.Views {
		language := spec.Language
		if language != analyzer.LanguageGo && language != analyzer.LanguageJavaScript {
			return protocol.Workspace{}, protocol.NewError(protocol.CodeInvalidParams, "unsupported language view", map[string]any{"language": language})
		}
		if seen[language] {
			return protocol.Workspace{}, protocol.NewError(protocol.CodeInvalidParams, "duplicate language view", map[string]any{"language": language})
		}
		if len(spec.BuildTags) > 0 && language != analyzer.LanguageGo {
			return protocol.Workspace{}, protocol.NewError(protocol.CodeInvalidParams, "buildTags apply only to the go view", map[string]any{"language": language})
		}

		seen[language] = true
		configs = append(configs, analyzer.Config{Root: root, Language: language, BuildTags: append([]string(nil), spec.BuildTags...)})
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()

	opened := &workspace{id: engine.identifier("workspace"), rootURI: protocol.FileURI(root), root: root, name: params.Name}
	for _, config := range configs {
		languageView := &view{id: engine.identifier("view-" + config.Language), workspace: opened, language: config.Language, config: config}
		opened.views = append(opened.views, languageView)
		engine.views[languageView.id] = languageView
	}
	engine.workspaces[opened.id] = opened

	return engine.workspaceModel(opened), nil
}

// workspaceModel renders a workspace and its view states. Callers hold mu.
func (engine *Engine) workspaceModel(opened *workspace) protocol.Workspace {
	views := make([]protocol.LanguageView, 0, len(opened.views))
	for _, languageView := range opened.views {
		views = append(views, protocol.LanguageView{
			ViewID:      languageView.id,
			WorkspaceID: opened.id,
			Language:    languageView.language,
			LoadState:   languageView.loadState(),
		})
	}

	return protocol.Workspace{WorkspaceID: opened.id, RootURI: opened.rootURI, Name: opened.name, Views: views}
}

// loadState derives the wire lifecycle state. Callers hold Engine.mu.
func (languageView *view) loadState() protocol.LoadState {
	state := protocol.LoadState{State: protocol.LoadStateUnscanned}
	if current := languageView.current(); current != nil {
		descriptor := current.descriptor
		state.State = protocol.LoadStateReady
		state.CurrentSnapshot = &descriptor
	}
	if languageView.lastFailure != nil {
		failure := *languageView.lastFailure
		state.State = protocol.LoadStateFailed
		state.Failure = &failure
	}
	if languageView.active != nil {
		state.State = protocol.LoadStateAnalyzing
		state.AnalysisID = languageView.active.id
	}
	return state
}

func (languageView *view) current() *snapshot {
	if len(languageView.snapshots) == 0 {
		return nil
	}
	return languageView.snapshots[len(languageView.snapshots)-1]
}

func (engine *Engine) getWorkspace(params protocol.WorkspaceGetParams) (protocol.Workspace, *protocol.Error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	opened := engine.workspaces[params.WorkspaceID]
	if opened == nil {
		return protocol.Workspace{}, workspaceNotFound(params.WorkspaceID)
	}
	return engine.workspaceModel(opened), nil
}

func (engine *Engine) closeWorkspace(params protocol.WorkspaceCloseParams) *protocol.Error {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	opened := engine.workspaces[params.WorkspaceID]
	if opened == nil {
		return workspaceNotFound(params.WorkspaceID)
	}

	engine.closeWorkspaceLocked(opened)
	return nil
}

// closeWorkspaceLocked cancels analyses and evicts every snapshot. Callers hold mu.
func (engine *Engine) closeWorkspaceLocked(opened *workspace) {
	for _, languageView := range opened.views {
		languageView.closed = true
		if languageView.active != nil {
			languageView.active.cancel()
		}
		languageView.snapshots = nil
		delete(engine.views, languageView.id)
	}
	delete(engine.workspaces, opened.id)
}

// startAnalysis registers an analysis and returns a launcher that the
// dispatcher runs after the start response is written, so a client always
// receives the analysisId before any notification that mentions it.
func (engine *Engine) startAnalysis(params protocol.AnalysisStartParams) (protocol.AnalysisStartResult, func(), *protocol.Error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	languageView := engine.views[params.ViewID]
	if languageView == nil {
		return protocol.AnalysisStartResult{}, nil, viewNotFound(params.ViewID)
	}
	if err := validateHint(params.IncrementalHint, languageView.workspace.root); err != nil {
		return protocol.AnalysisStartResult{}, nil, err
	}
	if languageView.active != nil {
		return protocol.AnalysisStartResult{}, nil, protocol.NewError(protocol.CodeAnalysisAlreadyRunning, "an analysis is already running for this view", map[string]any{"viewId": languageView.id, "analysisId": languageView.active.id})
	}

	analysisContext, cancel := context.WithCancel(engine.ctx)
	started := &analysis{id: engine.identifier("analysis"), view: languageView, ctx: analysisContext, cancel: cancel}
	languageView.active = started
	engine.analyses[started.id] = started

	launch := func() { go engine.runAnalysis(started) }
	return protocol.AnalysisStartResult{AnalysisID: started.id}, launch, nil
}

// validateHint rejects malformed hints. The engine always performs a full
// analysis, so a valid hint never changes results.
func validateHint(hint *protocol.IncrementalHint, root string) *protocol.Error {
	if hint == nil {
		return nil
	}

	for _, changed := range hint.ChangedURIs {
		path, err := protocol.PathFromURI(changed)
		if err != nil {
			return protocol.Errorf(protocol.CodeInvalidParams, "incrementalHint.changedUris: %v", err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return protocol.NewError(protocol.CodeInvalidParams, "incrementalHint.changedUris must be beneath the workspace root", map[string]any{"uri": changed})
		}
	}
	return nil
}

// runAnalysis builds a replacement index and either publishes it atomically
// or reports a failure, leaving the previous snapshot queryable.
// Side Effect (Edge): runs language backends and Git through Options.Analyze.
func (engine *Engine) runAnalysis(started *analysis) {
	languageView := started.view
	defer started.cancel()

	languageView.emit.Lock()
	engine.notify(protocol.MethodAnalysisProgress, protocol.AnalysisProgress{
		AnalysisID: started.id,
		ViewID:     languageView.id,
		Phase:      "analyzing",
		Message:    "Analyzing " + languageView.language + " view",
	})
	languageView.emit.Unlock()

	index, err := engine.analyzeSafely(started.ctx, languageView.config)

	languageView.emit.Lock()
	defer languageView.emit.Unlock()

	published, failure := engine.commit(started, index, err)
	if failure != nil {
		engine.notify(protocol.MethodAnalysisFailed, protocol.AnalysisFailure{AnalysisID: started.id, ViewID: languageView.id, Failure: *failure})
		return
	}
	engine.notify(protocol.MethodAnalysisPublished, protocol.AnalysisPublished{AnalysisID: started.id, ViewID: languageView.id, Snapshot: published})
}

func (engine *Engine) analyzeSafely(ctx context.Context, config analyzer.Config) (index *analyzer.Index, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			engine.logger.Error("analysis panicked", "language", config.Language, "panic", recovered)
			err = &internalFailure{message: fmt.Sprint("analysis panicked: ", recovered)}
		}
	}()

	index, err = engine.options.Analyze(ctx, config)
	if err == nil && index == nil {
		err = &internalFailure{message: "analysis returned no index"}
	}
	return index, err
}

type internalFailure struct{ message string }

func (failure *internalFailure) Error() string { return failure.message }

// commit decides publication under the session lock so cancellation and
// publication are totally ordered.
func (engine *Engine) commit(started *analysis, index *analyzer.Index, analyzeErr error) (protocol.Snapshot, *protocol.Failure) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	languageView := started.view
	started.terminal = true
	languageView.active = nil

	if started.ctx.Err() != nil || languageView.closed {
		return protocol.Snapshot{}, &protocol.Failure{Kind: protocol.FailureCancelled, Message: "analysis was cancelled"}
	}
	if analyzeErr != nil {
		kind := protocol.FailureLoadFailed
		var internal *internalFailure
		if errors.As(analyzeErr, &internal) {
			kind = protocol.FailureInternal
		}
		failure := protocol.Failure{Kind: kind, Message: analyzeErr.Error()}
		languageView.lastFailure = &failure
		return protocol.Snapshot{}, &failure
	}

	languageView.lastFailure = nil
	languageView.revision++
	published := &snapshot{
		index: index,
		descriptor: protocol.Snapshot{
			SnapshotID:  engine.identifier("snapshot-" + languageView.language),
			ViewID:      languageView.id,
			Revision:    languageView.revision,
			SymbolCount: len(index.Functions),
			EdgeCount:   len(index.Edges),
		},
	}

	languageView.snapshots = append(languageView.snapshots, published)
	if excess := len(languageView.snapshots) - retainedSnapshotsPerView; excess > 0 {
		languageView.snapshots = append([]*snapshot(nil), languageView.snapshots[excess:]...)
	}
	return published.descriptor, nil
}

func (engine *Engine) cancelAnalysis(params protocol.AnalysisCancelParams) (protocol.AnalysisCancelResult, *protocol.Error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	started := engine.analyses[params.AnalysisID]
	if started == nil {
		return protocol.AnalysisCancelResult{}, protocol.NewError(protocol.CodeAnalysisNotFound, "analysis not found", map[string]any{"analysisId": params.AnalysisID})
	}
	if started.terminal {
		return protocol.AnalysisCancelResult{CancelRequested: false}, nil
	}

	started.cancel()
	return protocol.AnalysisCancelResult{CancelRequested: true}, nil
}

// resolveSnapshot finds an available snapshot that belongs to the view.
func (engine *Engine) resolveSnapshot(query protocol.SnapshotQuery) (*snapshot, string, *protocol.Error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	languageView := engine.views[query.ViewID]
	if languageView == nil {
		return nil, "", viewNotFound(query.ViewID)
	}
	for _, retained := range languageView.snapshots {
		if retained.descriptor.SnapshotID == query.SnapshotID {
			return retained, languageView.workspace.root, nil
		}
	}

	return nil, "", protocol.NewError(protocol.CodeSnapshotUnavailable, "snapshot is unavailable for this view", map[string]any{"viewId": query.ViewID, "snapshotId": query.SnapshotID})
}

func workspaceNotFound(workspaceID string) *protocol.Error {
	return protocol.NewError(protocol.CodeWorkspaceNotFound, "workspace not found", map[string]any{"workspaceId": workspaceID})
}

func viewNotFound(viewID string) *protocol.Error {
	return protocol.NewError(protocol.CodeViewNotFound, "view not found", map[string]any{"viewId": viewID})
}

// beginShutdown stops accepting work and cancels active analyses.
func (engine *Engine) beginShutdown() {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	engine.state = sessionShuttingDown
	for _, started := range engine.analyses {
		if !started.terminal {
			started.cancel()
		}
	}
}

// finishShutdown closes every workspace once in-flight handlers are done.
func (engine *Engine) finishShutdown() {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	for _, opened := range engine.workspaces {
		engine.closeWorkspaceLocked(opened)
	}
	engine.state = sessionShutdown
}
