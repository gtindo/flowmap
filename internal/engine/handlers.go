package engine

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gtindo/flowmap/internal/protocol"
)

// methods maps capability-independent request names to handlers. Lifecycle
// requests are routed separately because they change session state.
func (engine *Engine) methods() map[string]methodHandler {
	return map[string]methodHandler{
		protocol.MethodWorkspaceOpen:     engine.handleWorkspaceOpen,
		protocol.MethodWorkspaceClose:    engine.handleWorkspaceClose,
		protocol.MethodAnalysisStart:     engine.handleAnalysisStart,
		protocol.MethodAnalysisCancel:    engine.handleAnalysisCancel,
		protocol.MethodSymbolSearch:      engine.handleSymbolSearch,
		protocol.MethodSymbolGet:         engine.handleSymbolGet,
		protocol.MethodGraphNeighborhood: engine.handleGraphNeighborhood,
		protocol.MethodChangesList:       engine.handleChangesList,
		protocol.MethodDiagnosticsList:   engine.handleDiagnosticsList,
		protocol.MethodSymbolSummary:     engine.handleSymbolSummary,
	}
}

func (engine *Engine) handleWorkspaceOpen(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.WorkspaceOpenParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	opened, err := engine.openWorkspace(params)
	return handlerResult{value: opened, err: err}
}

func (engine *Engine) handleWorkspaceClose(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.WorkspaceCloseParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	return handlerResult{value: nil, err: engine.closeWorkspace(params)}
}

func (engine *Engine) handleAnalysisStart(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.AnalysisStartParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	result, launch, err := engine.startAnalysis(params)
	return handlerResult{value: result, after: launch, err: err}
}

func (engine *Engine) handleAnalysisCancel(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.AnalysisCancelParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	result, err := engine.cancelAnalysis(params)
	return handlerResult{value: result, err: err}
}

func (engine *Engine) handleSymbolSearch(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.SymbolSearchParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	selected, _, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	matches := searchSymbols(selected.index, params.Query, params.IncludeTests)
	binding := cursor{Method: protocol.MethodSymbolSearch, ViewID: params.ViewID, SnapshotID: params.SnapshotID, Key: params.Query + "\x00" + strconv.FormatBool(params.IncludeTests)}
	start, end, next, err := pageBounds(params.Page, binding, len(matches))
	if err != nil {
		return handlerResult{err: err}
	}

	return handlerResult{value: protocol.SymbolSearchResult{Items: matches[start:end], NextCursor: next}}
}

func (engine *Engine) handleSymbolGet(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.SymbolGetParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	selected, _, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	function, exists := selected.index.Function(params.SymbolID)
	if !exists {
		return handlerResult{err: symbolNotFound(params.SymbolID)}
	}

	return handlerResult{value: protocol.SymbolGetResult{Symbol: symbol(function)}}
}

func (engine *Engine) handleGraphNeighborhood(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.GraphNeighborhoodParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	switch params.Direction {
	case protocol.DirectionUpstream, protocol.DirectionDownstream, protocol.DirectionBoth:
	default:
		return handlerResult{err: protocol.NewError(protocol.CodeInvalidParams, "direction must be upstream, downstream, or both", map[string]any{"direction": params.Direction})}
	}
	if params.Depth == nil || *params.Depth < 0 || *params.Depth > protocol.MaxGraphDepth {
		return handlerResult{err: protocol.Errorf(protocol.CodeInvalidParams, "depth must be between 0 and %d", protocol.MaxGraphDepth)}
	}

	selected, root, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	focused, focusErr := selected.index.Focus(params.RootSymbolID, params.Direction, *params.Depth, params.IncludeTests)
	if focusErr != nil {
		return handlerResult{err: symbolNotFound(params.RootSymbolID)}
	}

	return handlerResult{value: graph(focused, root)}
}

func (engine *Engine) handleChangesList(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.ChangesListParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	selected, _, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	changes := gitChanges(selected.index)
	binding := cursor{Method: protocol.MethodChangesList, ViewID: params.ViewID, SnapshotID: params.SnapshotID}
	start, end, next, err := pageBounds(params.Page, binding, len(changes))
	if err != nil {
		return handlerResult{err: err}
	}

	return handlerResult{value: protocol.ChangesListResult{GitState: gitState(selected.index.Git), Items: changes[start:end], NextCursor: next}}
}

func (engine *Engine) handleDiagnosticsList(_ context.Context, raw json.RawMessage) handlerResult {
	var params protocol.DiagnosticsListParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	selected, root, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	report := selected.index.LoadReport
	items := diagnostics(report, root)
	binding := cursor{Method: protocol.MethodDiagnosticsList, ViewID: params.ViewID, SnapshotID: params.SnapshotID}
	start, end, next, err := pageBounds(params.Page, binding, len(items))
	if err != nil {
		return handlerResult{err: err}
	}

	return handlerResult{value: protocol.DiagnosticsListResult{LoadReport: loadReport(report, selected.index.Language), Items: items[start:end], NextCursor: next}}
}

// handleSymbolSummary generates or reuses a cached summary.
// Side Effect (Edge): may run the configured summarizer command and write the cache.
func (engine *Engine) handleSymbolSummary(ctx context.Context, raw json.RawMessage) handlerResult {
	if !engine.capabilities().SymbolSummary {
		return handlerResult{err: protocol.NewError(protocol.CodeCapabilityNotSupported, "symbol summaries are disabled; configure --summarizer-command", map[string]any{"capabilities": []string{protocol.CapabilitySymbolSummary}})}
	}

	var params protocol.SymbolSummaryParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	selected, _, err := engine.resolveSnapshot(params.SnapshotQuery)
	if err != nil {
		return handlerResult{err: err}
	}

	function, exists := selected.index.Function(params.SymbolID)
	if !exists {
		return handlerResult{err: symbolNotFound(params.SymbolID)}
	}

	summarizer, cache := engine.options.Summarizer, engine.options.SummaryCache
	request := SummaryRequest{QualifiedName: function.QualifiedName, Signature: function.Signature, Source: function.Source, Documentation: function.Intent, Contracts: function.Contracts}
	if summary, cached := cache.Get(summarizer.Identity(), request); cached {
		return handlerResult{value: protocol.SymbolSummaryResult{Summary: summary, Source: protocol.SummarySourceGenerated, Cached: true}}
	}

	summary, summarizeErr := summarizer.Summarize(ctx, request)
	if summarizeErr != nil {
		return handlerResult{err: protocol.Errorf(protocol.CodeInternalError, "%s", strings.TrimSpace(summarizeErr.Error()))}
	}
	if err := cache.Put(summarizer.Identity(), request, summary); err != nil {
		return handlerResult{err: protocol.Errorf(protocol.CodeInternalError, "%s", err.Error())}
	}

	return handlerResult{value: protocol.SymbolSummaryResult{Summary: summary, Source: protocol.SummarySourceGenerated}}
}

func symbolNotFound(symbolID string) *protocol.Error {
	return protocol.NewError(protocol.CodeSymbolNotFound, "symbol not found", map[string]any{"symbolId": symbolID})
}
