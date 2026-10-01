// Package protocol defines the experimental Flowmap engine protocol from
// docs/tdd/0001-flowmap-engine-protocol.md: wire models, JSON-RPC framing,
// and a client that any Go presentation adapter can use.
package protocol

// Version is the only exact protocol version this build negotiates.
const Version = "0"

// Method and notification names defined by protocol version 0.
const (
	MethodInitialize        = "initialize"
	MethodShutdown          = "shutdown"
	MethodExit              = "exit"
	MethodWorkspaceOpen     = "workspace/open"
	MethodWorkspaceClose    = "workspace/close"
	MethodAnalysisStart     = "analysis/start"
	MethodAnalysisCancel    = "analysis/cancel"
	MethodAnalysisProgress  = "analysis/progress"
	MethodAnalysisPublished = "analysis/published"
	MethodAnalysisFailed    = "analysis/failed"
	MethodSymbolSearch      = "symbol/search"
	MethodSymbolGet         = "symbol/get"
	MethodGraphNeighborhood = "graph/neighborhood"
	MethodChangesList       = "changes/list"
	MethodDiagnosticsList   = "diagnostics/list"
	MethodSymbolSummary     = "symbol/summary"
)

// Capability names that a version 0 client may require.
const (
	CapabilityLanguages     = "languages"
	CapabilitySymbolSummary = "symbolSummary"
)

// Language view load states.
const (
	LoadStateUnscanned = "unscanned"
	LoadStateAnalyzing = "analyzing"
	LoadStateReady     = "ready"
	LoadStateFailed    = "failed"
)

// Analysis failure kinds.
const (
	FailureCancelled  = "cancelled"
	FailureLoadFailed = "loadFailed"
	FailureInternal   = "internal"
)

// Graph traversal directions.
const (
	DirectionUpstream   = "upstream"
	DirectionDownstream = "downstream"
	DirectionBoth       = "both"
)

// Pagination and traversal bounds.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
	MaxGraphDepth    = 8
)

// SummarySourceGenerated marks machine-generated symbol summaries.
const SummarySourceGenerated = "generated"

// PeerInfo identifies a client or engine implementation.
type PeerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeParams negotiates the exact protocol version and capabilities.
type InitializeParams struct {
	ProtocolVersion      string   `json:"protocolVersion"`
	ClientInfo           PeerInfo `json:"clientInfo"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
}

// Capabilities advertises optional engine behavior.
type Capabilities struct {
	Languages     []string `json:"languages"`
	SymbolSummary bool     `json:"symbolSummary"`
}

// InitializeResult confirms the negotiated session.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	EngineInfo      PeerInfo     `json:"engineInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

// ViewSpec requests one language view inside a workspace.
type ViewSpec struct {
	Language  string   `json:"language"`
	BuildTags []string `json:"buildTags,omitempty"`
}

// WorkspaceOpenParams registers one repository root.
type WorkspaceOpenParams struct {
	RootURI string     `json:"rootUri"`
	Name    string     `json:"name,omitempty"`
	Views   []ViewSpec `json:"views"`
}

// WorkspaceCloseParams identifies the workspace to close.
type WorkspaceCloseParams struct {
	WorkspaceID string `json:"workspaceId"`
}

// Workspace is one opened repository root and its language views.
type Workspace struct {
	WorkspaceID string         `json:"workspaceId"`
	RootURI     string         `json:"rootUri"`
	Name        string         `json:"name,omitempty"`
	Views       []LanguageView `json:"views"`
}

// LanguageView is one independently analyzed language of a workspace.
type LanguageView struct {
	ViewID      string    `json:"viewId"`
	WorkspaceID string    `json:"workspaceId"`
	Language    string    `json:"language"`
	LoadState   LoadState `json:"loadState"`
}

// LoadState reports a view's analysis lifecycle.
type LoadState struct {
	State             string `json:"state"`
	AnalysisID        string `json:"analysisId,omitempty"`
	CurrentSnapshotID string `json:"currentSnapshotId,omitempty"`
}

// IncrementalHint is a correctness-neutral optimization hint.
type IncrementalHint struct {
	BaseSnapshotID string   `json:"baseSnapshotId,omitempty"`
	ChangedURIs    []string `json:"changedUris,omitempty"`
}

// AnalysisStartParams starts analysis of one language view.
type AnalysisStartParams struct {
	ViewID          string           `json:"viewId"`
	IncrementalHint *IncrementalHint `json:"incrementalHint,omitempty"`
}

// AnalysisStartResult identifies an accepted analysis.
type AnalysisStartResult struct {
	AnalysisID string `json:"analysisId"`
}

// AnalysisCancelParams identifies the analysis to cancel.
type AnalysisCancelParams struct {
	AnalysisID string `json:"analysisId"`
}

// AnalysisCancelResult acknowledges a best-effort cancellation.
type AnalysisCancelResult struct {
	CancelRequested bool `json:"cancelRequested"`
}

// AnalysisProgress is a non-authoritative progress notification.
type AnalysisProgress struct {
	AnalysisID string `json:"analysisId"`
	ViewID     string `json:"viewId"`
	Phase      string `json:"phase"`
	Message    string `json:"message,omitempty"`
	Completed  *int   `json:"completed,omitempty"`
	Total      *int   `json:"total,omitempty"`
}

// AnalysisPublished announces an atomically installed snapshot.
type AnalysisPublished struct {
	AnalysisID string   `json:"analysisId"`
	ViewID     string   `json:"viewId"`
	Snapshot   Snapshot `json:"snapshot"`
}

// AnalysisFailure announces an analysis that ended without publication.
type AnalysisFailure struct {
	AnalysisID string  `json:"analysisId"`
	ViewID     string  `json:"viewId"`
	Failure    Failure `json:"failure"`
}

// Failure describes why an analysis did not publish.
type Failure struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Snapshot describes one complete, immutable generation of a language view.
type Snapshot struct {
	SnapshotID  string `json:"snapshotId"`
	ViewID      string `json:"viewId"`
	Revision    int    `json:"revision"`
	SymbolCount int    `json:"symbolCount"`
	EdgeCount   int    `json:"edgeCount"`
}

// SnapshotQuery addresses one snapshot of one view.
type SnapshotQuery struct {
	ViewID     string `json:"viewId"`
	SnapshotID string `json:"snapshotId"`
}

// Page requests one page of a pageable result.
type Page struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// SymbolSearchParams searches named symbols in a snapshot.
type SymbolSearchParams struct {
	SnapshotQuery
	Query        string `json:"query,omitempty"`
	IncludeTests bool   `json:"includeTests,omitempty"`
	Page         *Page  `json:"page,omitempty"`
}

// SymbolSearchResult is one page of symbol summaries.
type SymbolSearchResult struct {
	Items      []SymbolSummary `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// SymbolGetParams addresses one symbol in a snapshot.
type SymbolGetParams struct {
	SnapshotQuery
	SymbolID string `json:"symbolId"`
}

// SymbolGetResult contains one full symbol.
type SymbolGetResult struct {
	Symbol Symbol `json:"symbol"`
}

// GraphNeighborhoodParams requests a bounded graph neighborhood.
type GraphNeighborhoodParams struct {
	SnapshotQuery
	RootSymbolID string `json:"rootSymbolId"`
	Direction    string `json:"direction"`
	Depth        *int   `json:"depth"`
	IncludeTests bool   `json:"includeTests,omitempty"`
}

// ChangesListParams requests a page of Git-changed symbols.
type ChangesListParams struct {
	SnapshotQuery
	Page *Page `json:"page,omitempty"`
}

// ChangesListResult is the captured Git state and one page of changes.
type ChangesListResult struct {
	GitState   GitState    `json:"gitState"`
	Items      []GitChange `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// DiagnosticsListParams requests a page of load diagnostics.
type DiagnosticsListParams struct {
	SnapshotQuery
	Page *Page `json:"page,omitempty"`
}

// DiagnosticsListResult is the captured load report and one page of diagnostics.
type DiagnosticsListResult struct {
	LoadReport LoadReport   `json:"loadReport"`
	Items      []Diagnostic `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// SymbolSummaryParams requests a generated summary for one symbol.
type SymbolSummaryParams struct {
	SnapshotQuery
	SymbolID string `json:"symbolId"`
}

// SymbolSummaryResult contains a generated or cached summary.
type SymbolSummaryResult struct {
	Summary string `json:"summary"`
	Source  string `json:"source"`
	Cached  bool   `json:"cached"`
}

// SourceLocation is a one-based, inclusive line span in a file URI.
type SourceLocation struct {
	URI       string `json:"uri"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// SymbolSummary is the compact symbol representation used by search.
type SymbolSummary struct {
	SymbolID       string `json:"symbolId"`
	Name           string `json:"name"`
	QualifiedName  string `json:"qualifiedName"`
	Namespace      string `json:"namespace"`
	Language       string `json:"language"`
	Signature      string `json:"signature"`
	Classification string `json:"classification"`
	Public         bool   `json:"public"`
	Test           bool   `json:"test"`
}

// Symbol is the full representation of one analyzed callable.
type Symbol struct {
	SymbolSummary
	Kind                 string         `json:"kind"`
	Location             SourceLocation `json:"location"`
	Parameters           []string       `json:"parameters"`
	Results              []string       `json:"results"`
	Contracts            []Contract     `json:"contracts"`
	Intent               string         `json:"intent,omitempty"`
	IntentSource         string         `json:"intentSource,omitempty"`
	Source               string         `json:"source,omitempty"`
	Anonymous            bool           `json:"anonymous"`
	ClassificationDetail Classification `json:"classificationDetail"`
	Change               *SymbolChange  `json:"change,omitempty"`
}

// SymbolChange is a symbol's captured local difference from Git HEAD.
type SymbolChange struct {
	Kind string `json:"kind"`
	Diff string `json:"diff"`
}

// Classification describes a symbol's relationship to side effects.
type Classification struct {
	Kind       string   `json:"kind"`
	Provenance string   `json:"provenance"`
	Evidence   []string `json:"evidence"`
}

// Contract describes a named type crossing a symbol boundary.
type Contract struct {
	Name    string          `json:"name"`
	Kind    string          `json:"kind"`
	Fields  []ContractField `json:"fields"`
	Methods []string        `json:"methods"`
}

// ContractField is one field of a structural contract.
type ContractField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Graph is a bounded neighborhood whose edge endpoints all appear in nodes.
type Graph struct {
	RootSymbolID string   `json:"rootSymbolId"`
	Nodes        []Symbol `json:"nodes"`
	Edges        []Edge   `json:"edges"`
}

// Edge is one call or dependency relationship between symbols.
type Edge struct {
	FromSymbolID string          `json:"fromSymbolId"`
	ToSymbolID   string          `json:"toSymbolId"`
	Kind         string          `json:"kind"`
	Dynamic      bool            `json:"dynamic"`
	CallSite     *SourceLocation `json:"callSite,omitempty"`
}

// GitState is the repository state captured during analysis.
type GitState struct {
	Available bool   `json:"available"`
	Detached  bool   `json:"detached"`
	Branch    string `json:"branch,omitempty"`
	Revision  string `json:"revision,omitempty"`
}

// GitChange is one symbol changed relative to Git HEAD.
type GitChange struct {
	SymbolID            string         `json:"symbolId"`
	QualifiedName       string         `json:"qualifiedName"`
	Namespace           string         `json:"namespace"`
	Location            SourceLocation `json:"location"`
	Test                bool           `json:"test"`
	Kind                string         `json:"kind"`
	Diff                string         `json:"diff"`
	LeafDescendantCount int            `json:"leafDescendantCount"`
}

// LoadReport summarizes source loading captured with a snapshot.
type LoadReport struct {
	Language        string   `json:"language"`
	TotalUnits      int      `json:"totalUnits"`
	FailedUnits     int      `json:"failedUnits"`
	BuildTags       []string `json:"buildTags"`
	DiagnosticCount int      `json:"diagnosticCount"`
}

// Diagnostic is one loading or analysis degradation captured with a snapshot.
type Diagnostic struct {
	Kind          string          `json:"kind"`
	Message       string          `json:"message"`
	AffectedUnits []string        `json:"affectedUnits"`
	Location      *SourceLocation `json:"location,omitempty"`
}
