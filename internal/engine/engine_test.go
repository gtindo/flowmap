package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gtindo/flowmap/internal/analyzer"
	"github.com/gtindo/flowmap/internal/protocol"
)

const (
	testRoot    = "/work/project"
	testTimeout = 5 * time.Second
)

// TestLifecycleNegotiation verifies initialization, version, and capability rules.
func TestLifecycleNegotiation(t *testing.T) {
	session := startRaw(t, Options{})

	session.send(`{"jsonrpc":"2.0","id":1,"method":"workspace/open","params":{}}`)
	session.expectError(1, protocol.CodeInvalidSessionState)

	session.send(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1","clientInfo":{"name":"test"}}}`)
	session.expectError(2, protocol.CodeProtocolVersionMismatch)

	session.send(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"0","clientInfo":{"name":"test"},"requiredCapabilities":["symbolSummary","telepathy"]}}`)
	response := session.expectError(3, protocol.CodeCapabilityNotSupported)
	if missing := fmt.Sprint(response.Error.Data["capabilities"]); missing != "[symbolSummary telepathy]" {
		t.Fatalf("missing capabilities = %s", missing)
	}

	session.send(`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"0","clientInfo":{"name":"test"},"requiredCapabilities":["languages"],"futureField":true}}`)
	initialized := session.read()
	var result protocol.InitializeResult
	if err := json.Unmarshal(initialized.Result, &result); err != nil || string(initialized.ID) != `"init"` || result.ProtocolVersion != "0" || result.Capabilities.SymbolSummary || len(result.Capabilities.Languages) != 2 {
		t.Fatalf("initialize = %s %s, %v", initialized.ID, initialized.Result, err)
	}

	session.send(`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"0","clientInfo":{"name":"test"}}}`)
	session.expectError(4, protocol.CodeInvalidSessionState)

	session.send(`{"jsonrpc":"2.0","id":5,"method":"shutdown"}`)
	if shutdown := session.read(); string(shutdown.Result) != "null" || shutdown.Error != nil {
		t.Fatalf("shutdown = %#v", shutdown)
	}
	session.send(`{"jsonrpc":"2.0","id":6,"method":"symbol/search","params":{}}`)
	session.expectError(6, protocol.CodeInvalidSessionState)

	session.send(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := session.wait(); err != nil {
		t.Fatalf("Serve() after shutdown and exit = %v", err)
	}
}

// TestWireErrors verifies JSON-RPC envelope validation and session survival.
func TestWireErrors(t *testing.T) {
	session := startRaw(t, Options{})

	session.sendFrame(`{"jsonrpc":`)
	if response := session.read(); response.Error == nil || response.Error.Code != protocol.CodeParseError || string(response.ID) != "null" {
		t.Fatalf("parse error response = %#v", response)
	}

	session.send(`[{"jsonrpc":"2.0","id":1,"method":"shutdown"}]`)
	session.expectErrorID("null", protocol.CodeInvalidRequest)

	session.send(`{"jsonrpc":"2.0","id":1.5,"method":"shutdown"}`)
	session.expectErrorID("null", protocol.CodeInvalidRequest)

	session.send(`{"jsonrpc":"1.0","id":7,"method":"shutdown"}`)
	session.expectError(7, protocol.CodeInvalidRequest)

	session.send(`{"jsonrpc":"2.0","id":8,"method":"no/such"}`)
	session.expectError(8, protocol.CodeMethodNotFound)

	session.send(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := session.wait(); !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("exit without shutdown = %v", err)
	}
}

// TestFramingFailureTerminatesSession verifies that unsynchronizable input ends the session.
func TestFramingFailureTerminatesSession(t *testing.T) {
	session := startRaw(t, Options{})
	if _, err := io.WriteString(session.input, "Content-Length: nope\r\n\r\n{}"); err != nil {
		t.Fatal(err)
	}
	if err := session.wait(); !errors.Is(err, protocol.ErrFraming) {
		t.Fatalf("Serve() = %v, want framing error", err)
	}
}

// TestStartResponsePrecedesNotifications verifies clients learn analysisId first.
func TestStartResponsePrecedesNotifications(t *testing.T) {
	session := startRaw(t, Options{Analyze: staticAnalyze(fixtureIndex("sample.Root"))})
	session.initialize()

	session.send(`{"jsonrpc":"2.0","id":2,"method":"workspace/open","params":{"rootUri":"file:///work/project","views":[{"language":"go"}]}}`)
	var opened protocol.Workspace
	if err := json.Unmarshal(session.read().Result, &opened); err != nil {
		t.Fatal(err)
	}

	session.send(`{"jsonrpc":"2.0","id":3,"method":"analysis/start","params":{"viewId":"` + opened.Views[0].ViewID + `"}}`)
	methods := []string{}
	for len(methods) < 3 {
		message := session.read()
		if message.IsResponse() {
			methods = append(methods, "response")
			continue
		}
		methods = append(methods, message.Method)
	}
	if strings.Join(methods, ",") != "response,analysis/progress,analysis/published" {
		t.Fatalf("message order = %v", methods)
	}
}

// TestWorkspaceValidation verifies workspace/open parameter rules.
func TestWorkspaceValidation(t *testing.T) {
	client := startClient(t, Options{})
	ctx := testContext(t)

	cases := map[string]protocol.WorkspaceOpenParams{
		"relative root":      {RootURI: "file:relative", Views: []protocol.ViewSpec{{Language: "go"}}},
		"non-file root":      {RootURI: "https://example.com/repo", Views: []protocol.ViewSpec{{Language: "go"}}},
		"no views":           {RootURI: "file:///work/project"},
		"duplicate views":    {RootURI: "file:///work/project", Views: []protocol.ViewSpec{{Language: "go"}, {Language: "go"}}},
		"unsupported":        {RootURI: "file:///work/project", Views: []protocol.ViewSpec{{Language: "rust"}}},
		"inapplicable flags": {RootURI: "file:///work/project", Views: []protocol.ViewSpec{{Language: "javascript", BuildTags: []string{"x"}}}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.OpenWorkspace(ctx, params)
			expectCode(t, err, protocol.CodeInvalidParams)
		})
	}

	first, err := client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: "file:///work/project", Name: "acme", Views: []protocol.ViewSpec{{Language: "go", BuildTags: []string{"integration"}}, {Language: "javascript"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: "file:///work/project", Views: []protocol.ViewSpec{{Language: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspaceID == second.WorkspaceID || first.Views[0].ViewID == second.Views[0].ViewID || first.Views[0].LoadState.State != protocol.LoadStateUnscanned || first.Name != "acme" {
		t.Fatalf("workspaces = %#v %#v", first, second)
	}

	if err := client.CloseWorkspace(ctx, first.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, client.CloseWorkspace(ctx, first.WorkspaceID), protocol.CodeWorkspaceNotFound)
	_, err = client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: first.Views[0].ViewID})
	expectCode(t, err, protocol.CodeViewNotFound)
}

// TestSnapshotPublicationRetentionAndQueries verifies immutable snapshots and query models.
func TestSnapshotPublicationRetentionAndQueries(t *testing.T) {
	var mu sync.Mutex
	generation := 0
	analyze := func(_ context.Context, config analyzer.Config) (*analyzer.Index, error) {
		mu.Lock()
		defer mu.Unlock()
		generation++
		if config.Root != testRoot || len(config.BuildTags) != 1 || config.BuildTags[0] != "integration" {
			return nil, fmt.Errorf("unexpected config %#v", config)
		}
		return fixtureIndex(fmt.Sprintf("sample.Root%d", generation)), nil
	}
	harness := newHarness(t, Options{Analyze: analyze})
	ctx := testContext(t)
	viewID := harness.openGo(ctx, []string{"integration"})

	first := harness.publish(ctx, viewID)
	if first.Revision != 1 || first.SymbolCount != 4 || first.EdgeCount != 2 {
		t.Fatalf("first snapshot = %#v", first)
	}
	query := protocol.SnapshotQuery{ViewID: viewID, SnapshotID: first.SnapshotID}

	search, err := harness.client.SearchSymbols(ctx, protocol.SymbolSearchParams{SnapshotQuery: query, Query: "ROOT"})
	if err != nil || len(search.Items) != 1 || search.Items[0].QualifiedName != "sample.Root1" || !search.Items[0].Public || search.NextCursor != "" {
		t.Fatalf("search = %#v, %v", search, err)
	}

	symbol, err := harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"})
	if err != nil || symbol.Kind != "function" || symbol.Location.URI != "file:///work/project/sample.go" || symbol.Location.StartLine != 10 || symbol.Location.EndLine != 14 || symbol.Change == nil || symbol.Change.Kind != "updated" || symbol.ClassificationDetail.Evidence == nil || symbol.Contracts == nil {
		t.Fatalf("symbol = %#v, %v", symbol, err)
	}
	_, err = harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "missing"})
	expectCode(t, err, protocol.CodeSymbolNotFound)

	depth := 1
	neighborhood, err := harness.client.Neighborhood(ctx, protocol.GraphNeighborhoodParams{SnapshotQuery: query, RootSymbolID: "root", Direction: protocol.DirectionDownstream, Depth: &depth})
	if err != nil || neighborhood.RootSymbolID != "root" || len(neighborhood.Nodes) != 2 || len(neighborhood.Edges) != 1 {
		t.Fatalf("neighborhood = %#v, %v", neighborhood, err)
	}
	if callSite := neighborhood.Edges[0].CallSite; callSite == nil || callSite.URI != "file:///work/project/sample.go" || callSite.StartLine != 12 {
		t.Fatalf("call site = %#v", callSite)
	}
	for name, params := range map[string]protocol.GraphNeighborhoodParams{
		"direction": {SnapshotQuery: query, RootSymbolID: "root", Direction: "sideways", Depth: &depth},
		"depth":     {SnapshotQuery: query, RootSymbolID: "root", Direction: protocol.DirectionBoth, Depth: intPointer(9)},
		"no depth":  {SnapshotQuery: query, RootSymbolID: "root", Direction: protocol.DirectionBoth},
	} {
		_, err := harness.client.Neighborhood(ctx, params)
		if !hasCode(err, protocol.CodeInvalidParams) {
			t.Fatalf("%s validation error = %v", name, err)
		}
	}

	changes, err := harness.client.ListChanges(ctx, protocol.ChangesListParams{SnapshotQuery: query})
	if err != nil || !changes.GitState.Available || changes.GitState.Branch != "main" || len(changes.Items) != 1 || changes.Items[0].LeafDescendantCount != 2 || !strings.Contains(changes.Items[0].Diff, "+new") {
		t.Fatalf("changes = %#v, %v", changes, err)
	}

	diagnostics, err := harness.client.ListDiagnostics(ctx, protocol.DiagnosticsListParams{SnapshotQuery: query})
	if err != nil || diagnostics.LoadReport.TotalUnits != 4 || diagnostics.LoadReport.FailedUnits != 1 || diagnostics.LoadReport.DiagnosticCount != 1 || len(diagnostics.Items) != 1 {
		t.Fatalf("diagnostics = %#v, %v", diagnostics, err)
	}
	if location := diagnostics.Items[0].Location; location == nil || location.URI != "file:///work/project/broken/broken.go" || location.StartLine != 3 {
		t.Fatalf("diagnostic location = %#v", location)
	}

	second := harness.publish(ctx, viewID)
	if second.Revision != 2 || second.SnapshotID == first.SnapshotID {
		t.Fatalf("second snapshot = %#v", second)
	}
	if _, err := harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"}); err != nil {
		t.Fatalf("predecessor snapshot evicted too early: %v", err)
	}

	harness.publish(ctx, viewID)
	_, err = harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"})
	expectCode(t, err, protocol.CodeSnapshotUnavailable)

	otherView := harness.openGo(ctx, []string{"integration"})
	_, err = harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: protocol.SnapshotQuery{ViewID: otherView, SnapshotID: second.SnapshotID}, SymbolID: "root"})
	expectCode(t, err, protocol.CodeSnapshotUnavailable)
}

// TestPagination verifies opaque, request-bound cursors.
func TestPagination(t *testing.T) {
	harness := newHarness(t, Options{Analyze: staticAnalyze(fixtureIndex("sample.Root"))})
	ctx := testContext(t)
	viewID := harness.openGo(ctx, nil)
	published := harness.publish(ctx, viewID)
	query := protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}

	names := []string{}
	params := protocol.SymbolSearchParams{SnapshotQuery: query, IncludeTests: true, Page: &protocol.Page{Limit: 1}}
	for {
		page, err := harness.client.SearchSymbols(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			names = append(names, item.QualifiedName)
		}
		if page.NextCursor == "" {
			break
		}
		params.Page.Cursor = page.NextCursor
	}
	if strings.Join(names, ",") != "sample.Helper,sample.Root,sample.TestRoot" {
		t.Fatalf("paged names = %v", names)
	}

	first, err := harness.client.SearchSymbols(ctx, protocol.SymbolSearchParams{SnapshotQuery: query, Page: &protocol.Page{Limit: 1}})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	_, err = harness.client.SearchSymbols(ctx, protocol.SymbolSearchParams{SnapshotQuery: query, Query: "other", Page: &protocol.Page{Limit: 1, Cursor: first.NextCursor}})
	expectCode(t, err, protocol.CodeInvalidParams)
	_, err = harness.client.ListChanges(ctx, protocol.ChangesListParams{SnapshotQuery: query, Page: &protocol.Page{Cursor: first.NextCursor}})
	expectCode(t, err, protocol.CodeInvalidParams)
	_, err = harness.client.SearchSymbols(ctx, protocol.SymbolSearchParams{SnapshotQuery: query, Page: &protocol.Page{Limit: 201}})
	expectCode(t, err, protocol.CodeInvalidParams)
}

// TestAnalysisFailureCancellationAndOverlap verifies best-effort analysis control.
func TestAnalysisFailureCancellationAndOverlap(t *testing.T) {
	started := make(chan struct{}, 4)
	var mu sync.Mutex
	mode := "ok"
	analyze := func(ctx context.Context, _ analyzer.Config) (*analyzer.Index, error) {
		mu.Lock()
		current := mode
		mu.Unlock()
		started <- struct{}{}
		switch current {
		case "block":
			<-ctx.Done()
			return nil, ctx.Err()
		case "fail":
			return nil, fmt.Errorf("broken source")
		case "panic":
			panic("backend bug")
		default:
			return fixtureIndex("sample.Root"), nil
		}
	}
	setMode := func(next string) {
		mu.Lock()
		mode = next
		mu.Unlock()
	}

	harness := newHarness(t, Options{Analyze: analyze})
	ctx := testContext(t)
	viewID := harness.openGo(ctx, nil)
	published := harness.publish(ctx, viewID)
	<-started

	setMode("block")
	blocked, err := harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	_, err = harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	expectCode(t, err, protocol.CodeAnalysisAlreadyRunning)

	query := protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}
	if _, err := harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"}); err != nil {
		t.Fatalf("current snapshot unavailable during analysis: %v", err)
	}

	requested, err := harness.client.CancelAnalysis(ctx, blocked)
	if err != nil || !requested {
		t.Fatalf("cancel = %t, %v", requested, err)
	}
	if failure := harness.awaitFailure(ctx, blocked); failure.Kind != protocol.FailureCancelled {
		t.Fatalf("cancel failure = %#v", failure)
	}
	requested, err = harness.client.CancelAnalysis(ctx, blocked)
	if err != nil || requested {
		t.Fatalf("terminal cancel = %t, %v", requested, err)
	}
	_, err = harness.client.CancelAnalysis(ctx, "analysis-unknown")
	expectCode(t, err, protocol.CodeAnalysisNotFound)

	setMode("fail")
	failed, err := harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	if err != nil {
		t.Fatal(err)
	}
	if failure := harness.awaitFailure(ctx, failed); failure.Kind != protocol.FailureLoadFailed || failure.Message != "broken source" {
		t.Fatalf("load failure = %#v", failure)
	}

	setMode("panic")
	panicked, err := harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	if err != nil {
		t.Fatal(err)
	}
	if failure := harness.awaitFailure(ctx, panicked); failure.Kind != protocol.FailureInternal {
		t.Fatalf("panic failure = %#v", failure)
	}

	if _, err := harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"}); err != nil {
		t.Fatalf("failed analyses invalidated the published snapshot: %v", err)
	}

	_, err = harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID, IncrementalHint: &protocol.IncrementalHint{ChangedURIs: []string{"file:///elsewhere/x.go"}}})
	expectCode(t, err, protocol.CodeInvalidParams)
}

// TestSymbolSummaryCapability verifies the optional summary method and cache.
func TestSymbolSummaryCapability(t *testing.T) {
	disabled := newHarness(t, Options{Analyze: staticAnalyze(fixtureIndex("sample.Root"))})
	ctx := testContext(t)
	viewID := disabled.openGo(ctx, nil)
	published := disabled.publish(ctx, viewID)
	_, err := disabled.client.SummarizeSymbol(ctx, protocol.SymbolSummaryParams{SnapshotQuery: protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}, SymbolID: "root"})
	expectCode(t, err, protocol.CodeCapabilityNotSupported)

	cache, err := NewSummaryCacheIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	enabled := newHarness(t, Options{Analyze: staticAnalyze(fixtureIndex("sample.Root")), Summarizer: CommandSummarizer{Command: `printf '{"summary":"intent"}'`}, SummaryCache: cache})
	if !enabled.client.Session().Capabilities.SymbolSummary {
		t.Fatal("symbolSummary capability not advertised")
	}
	viewID = enabled.openGo(ctx, nil)
	published = enabled.publish(ctx, viewID)
	params := protocol.SymbolSummaryParams{SnapshotQuery: protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}, SymbolID: "root"}
	generated, err := enabled.client.SummarizeSymbol(ctx, params)
	if err != nil || generated.Summary != "intent" || generated.Source != protocol.SummarySourceGenerated || generated.Cached {
		t.Fatalf("summary = %#v, %v", generated, err)
	}
	cached, err := enabled.client.SummarizeSymbol(ctx, params)
	if err != nil || !cached.Cached {
		t.Fatalf("cached summary = %#v, %v", cached, err)
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
	cache, err := NewSummaryCacheIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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

func TestPositionLocation(t *testing.T) {
	cases := []struct {
		position string
		uri      string
		line     int
	}{
		{"/work/project/a.go:12", "file:///work/project/a.go", 12},
		{"pkg/a.go:7:3", "file:///work/project/pkg/a.go", 7},
		{"src/app.ts", "", 0},
		{"-", "", 0},
		{"", "", 0},
		{"a.go:0", "", 0},
	}
	for _, testCase := range cases {
		location := positionLocation(testRoot, testCase.position)
		if testCase.uri == "" {
			if location != nil {
				t.Fatalf("positionLocation(%q) = %#v, want nil", testCase.position, location)
			}
			continue
		}
		if location == nil || location.URI != testCase.uri || location.StartLine != testCase.line || location.EndLine != testCase.line {
			t.Fatalf("positionLocation(%q) = %#v", testCase.position, location)
		}
	}
}

// harness drives an in-process engine through the protocol client.
type harness struct {
	t      *testing.T
	client *protocol.Client

	mu       sync.Mutex
	outcomes map[string]chan json.RawMessage
}

func newHarness(t *testing.T, options Options) *harness {
	t.Helper()

	result := &harness{t: t, outcomes: make(map[string]chan json.RawMessage)}
	result.client = startClient(t, options)
	result.client.OnNotification(func(method string, params json.RawMessage) {
		if method != protocol.MethodAnalysisPublished && method != protocol.MethodAnalysisFailed {
			return
		}
		var envelope struct {
			AnalysisID string `json:"analysisId"`
		}
		_ = json.Unmarshal(params, &envelope)
		result.outcome(envelope.AnalysisID) <- append(json.RawMessage(method+"\n"), params...)
	})
	return result
}

func (harness *harness) outcome(analysisID string) chan json.RawMessage {
	harness.mu.Lock()
	defer harness.mu.Unlock()

	if harness.outcomes[analysisID] == nil {
		harness.outcomes[analysisID] = make(chan json.RawMessage, 1)
	}
	return harness.outcomes[analysisID]
}

func (harness *harness) openGo(ctx context.Context, tags []string) string {
	harness.t.Helper()

	opened, err := harness.client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: protocol.FileURI(testRoot), Views: []protocol.ViewSpec{{Language: "go", BuildTags: tags}}})
	if err != nil {
		harness.t.Fatal(err)
	}
	return opened.Views[0].ViewID
}

func (harness *harness) await(ctx context.Context, analysisID string) (string, json.RawMessage) {
	harness.t.Helper()

	select {
	case message := <-harness.outcome(analysisID):
		method, params, _ := strings.Cut(string(message), "\n")
		return method, json.RawMessage(params)
	case <-ctx.Done():
		harness.t.Fatalf("analysis %s did not finish", analysisID)
		return "", nil
	}
}

func (harness *harness) publish(ctx context.Context, viewID string) protocol.Snapshot {
	harness.t.Helper()

	analysisID, err := harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	if err != nil {
		harness.t.Fatal(err)
	}
	method, params := harness.await(ctx, analysisID)
	if method != protocol.MethodAnalysisPublished {
		harness.t.Fatalf("analysis ended with %s %s", method, params)
	}
	var published protocol.AnalysisPublished
	if err := json.Unmarshal(params, &published); err != nil {
		harness.t.Fatal(err)
	}
	return published.Snapshot
}

func (harness *harness) awaitFailure(ctx context.Context, analysisID string) protocol.Failure {
	harness.t.Helper()

	method, params := harness.await(ctx, analysisID)
	if method != protocol.MethodAnalysisFailed {
		harness.t.Fatalf("analysis ended with %s %s", method, params)
	}
	var failed protocol.AnalysisFailure
	if err := json.Unmarshal(params, &failed); err != nil {
		harness.t.Fatal(err)
	}
	return failed.Failure
}

func startClient(t *testing.T, options Options) *protocol.Client {
	t.Helper()

	connection, err := StartInProcess(testContext(t), options, protocol.PeerInfo{Name: "engine-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if err := connection.Close(ctx); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return connection.Client
}

// rawSession speaks framed JSON directly to exercise wire-level rules.
type rawSession struct {
	t      *testing.T
	input  *io.PipeWriter
	reader *protocol.FrameReader
	writer *protocol.FrameWriter
	served chan error
}

func startRaw(t *testing.T, options Options) *rawSession {
	t.Helper()

	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- New(options).Serve(context.Background(), inputReader, outputWriter)
		_ = outputWriter.Close()
		_ = inputReader.Close()
	}()
	t.Cleanup(func() { _ = inputWriter.Close() })

	return &rawSession{t: t, input: inputWriter, reader: protocol.NewFrameReader(outputReader), writer: protocol.NewFrameWriter(inputWriter), served: served}
}

func (session *rawSession) initialize() {
	session.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"0","clientInfo":{"name":"raw"}}}`)
	if response := session.read(); response.Error != nil {
		session.t.Fatalf("initialize = %#v", response.Error)
	}
}

func (session *rawSession) send(payload string) {
	session.t.Helper()
	session.sendFrame(payload)
}

func (session *rawSession) sendFrame(payload string) {
	session.t.Helper()
	if err := session.writer.Write([]byte(payload)); err != nil {
		session.t.Fatal(err)
	}
}

func (session *rawSession) read() protocol.Message {
	session.t.Helper()

	type frame struct {
		payload []byte
		err     error
	}
	frames := make(chan frame, 1)
	go func() {
		payload, err := session.reader.Read()
		frames <- frame{payload, err}
	}()

	select {
	case received := <-frames:
		if received.err != nil {
			session.t.Fatalf("read frame: %v", received.err)
		}
		var message protocol.Message
		if err := json.Unmarshal(received.payload, &message); err != nil {
			session.t.Fatalf("decode %s: %v", received.payload, err)
		}
		return message
	case <-time.After(testTimeout):
		session.t.Fatal("timed out waiting for engine output")
		return protocol.Message{}
	}
}

func (session *rawSession) expectError(id int, code int) protocol.Message {
	session.t.Helper()
	return session.expectErrorID(fmt.Sprint(id), code)
}

func (session *rawSession) expectErrorID(id string, code int) protocol.Message {
	session.t.Helper()

	response := session.read()
	if string(response.ID) != id || response.Error == nil || response.Error.Code != code {
		session.t.Fatalf("response = id %s error %#v, want id %s code %d", response.ID, response.Error, id, code)
	}
	return response
}

func (session *rawSession) wait() error {
	session.t.Helper()

	select {
	case err := <-session.served:
		return err
	case <-time.After(testTimeout):
		session.t.Fatal("engine did not exit")
		return nil
	}
}

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

func staticAnalyze(index *analyzer.Index) AnalyzeFunc {
	return func(context.Context, analyzer.Config) (*analyzer.Index, error) { return index, nil }
}

func expectCode(t *testing.T, err error, code int) {
	t.Helper()
	if !hasCode(err, code) {
		t.Fatalf("error = %v, want code %d", err, code)
	}
}

func hasCode(err error, code int) bool {
	var rpcError *protocol.Error
	return errors.As(err, &rpcError) && rpcError.Code == code
}

func intPointer(value int) *int {
	return &value
}

// fixtureIndex returns a small graph with a changed root, a closure, a test,
// and one load diagnostic.
func fixtureIndex(rootName string) *analyzer.Index {
	file := testRoot + "/sample.go"
	root := analyzer.Function{ID: "root", Name: "Root", Kind: "function", QualifiedName: rootName, Package: "sample", Language: "go", File: file, Line: 10, EndLine: 14, Public: true, Classification: analyzer.Classification{Kind: "pure", Provenance: "inferred"}, Change: &analyzer.FunctionChange{Kind: "updated", Diff: "-old\n+new\n"}}
	helper := analyzer.Function{ID: "helper", Name: "Helper", Kind: "function", QualifiedName: "sample.Helper", Package: "sample", Language: "go", File: file, Line: 20, EndLine: 22, Classification: analyzer.Classification{Kind: "unknown"}}
	closure := analyzer.Function{ID: "closure", Name: "Root$1", QualifiedName: rootName + "$1", Package: "sample", Language: "go", File: file, Line: 12, EndLine: 12, Anonymous: true, Classification: analyzer.Classification{Kind: "unknown"}}
	test := analyzer.Function{ID: "test", Name: "TestRoot", Kind: "function", QualifiedName: "sample.TestRoot", Package: "sample", Language: "go", File: testRoot + "/sample_test.go", Line: 5, EndLine: 7, Test: true, Classification: analyzer.Classification{Kind: "unknown"}}
	edges := []analyzer.Edge{
		{CallerID: "root", CalleeID: "closure", Kind: "call", CallSite: file + ":12"},
		{CallerID: "test", CalleeID: "root", Kind: "call", CallSite: testRoot + "/sample_test.go:6"},
	}

	index := &analyzer.Index{
		Root:      testRoot,
		Language:  "go",
		Functions: map[string]analyzer.Function{"root": root, "helper": helper, "closure": closure, "test": test},
		Edges:     edges,
		Outgoing:  map[string][]analyzer.Edge{},
		Incoming:  map[string][]analyzer.Edge{},
		LoadReport: analyzer.LoadReport{Root: testRoot, Language: "go", TotalPackageVariants: 4, FailedPackageVariants: 1, TotalUnits: 4, FailedUnits: 1, Diagnostics: []analyzer.LoadDiagnostic{
			{Kind: "type", Position: "broken/broken.go:3:9", Message: "undefined: missing", Packages: []string{"example.com/broken"}, Units: []string{"example.com/broken"}},
		}},
		Git: analyzer.GitSnapshot{Available: true, Branch: "main", Revision: "1234567890", ChangedFunctions: []analyzer.ChangedFunction{{ID: "root", QualifiedName: rootName, Package: "sample", File: file, Line: 10, Kind: "updated", LeafDescendantCount: 2}}},
	}
	for _, edge := range edges {
		index.Outgoing[edge.CallerID] = append(index.Outgoing[edge.CallerID], edge)
		index.Incoming[edge.CalleeID] = append(index.Incoming[edge.CalleeID], edge)
	}
	return index
}

// TestCloseWorkspaceCancelsActiveAnalysis verifies close cancels work and
// invalidates the workspace's views.
func TestCloseWorkspaceCancelsActiveAnalysis(t *testing.T) {
	started := make(chan struct{}, 2)
	var blocking atomicFlag
	analyze := func(ctx context.Context, _ analyzer.Config) (*analyzer.Index, error) {
		started <- struct{}{}
		if blocking.get() {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return fixtureIndex("sample.Root"), nil
	}
	harness := newHarness(t, Options{Analyze: analyze})
	ctx := testContext(t)

	opened, err := harness.client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: protocol.FileURI(testRoot), Views: []protocol.ViewSpec{{Language: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	viewID := opened.Views[0].ViewID
	published := harness.publish(ctx, viewID)
	<-started

	blocking.set(true)
	analysisID, err := harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	if err != nil {
		t.Fatal(err)
	}
	<-started

	if err := harness.client.CloseWorkspace(ctx, opened.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if failure := harness.awaitFailure(ctx, analysisID); failure.Kind != protocol.FailureCancelled {
		t.Fatalf("failure after close = %#v", failure)
	}

	_, err = harness.client.GetSymbol(ctx, protocol.SymbolGetParams{SnapshotQuery: protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}, SymbolID: "root"})
	expectCode(t, err, protocol.CodeViewNotFound)
	_, err = harness.client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: viewID})
	expectCode(t, err, protocol.CodeViewNotFound)
}

// TestShutdownCancelsActiveAnalysis verifies shutdown does not wait for an
// analysis to finish on its own.
func TestShutdownCancelsActiveAnalysis(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	analyze := func(ctx context.Context, _ analyzer.Config) (*analyzer.Index, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}

	ctx := testContext(t)
	connection, err := StartInProcess(ctx, Options{Analyze: analyze}, protocol.PeerInfo{Name: "shutdown-test"})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := connection.Client.OpenWorkspace(ctx, protocol.WorkspaceOpenParams{RootURI: protocol.FileURI(testRoot), Views: []protocol.ViewSpec{{Language: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Client.StartAnalysis(ctx, protocol.AnalysisStartParams{ViewID: opened.Views[0].ViewID}); err != nil {
		t.Fatal(err)
	}
	<-started

	if err := connection.Close(ctx); err != nil {
		t.Fatalf("Close() during analysis = %v", err)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("shutdown did not cancel the active analysis")
	}
	select {
	case <-connection.Client.Done():
	case <-ctx.Done():
		t.Fatal("engine stream stayed open after exit")
	}
}

// TestChangesAndDiagnosticsPaginate verifies cursors for every pageable method.
func TestChangesAndDiagnosticsPaginate(t *testing.T) {
	harness := newHarness(t, Options{Analyze: staticAnalyze(bulkIndex(5, 3))})
	ctx := testContext(t)
	viewID := harness.openGo(ctx, nil)
	published := harness.publish(ctx, viewID)
	query := protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}

	changeIDs := []string{}
	changeParams := protocol.ChangesListParams{SnapshotQuery: query, Page: &protocol.Page{Limit: 2}}
	for pages := 0; ; pages++ {
		page, err := harness.client.ListChanges(ctx, changeParams)
		if err != nil || len(page.Items) > 2 || !page.GitState.Available {
			t.Fatalf("changes page = %#v, %v", page, err)
		}
		for _, item := range page.Items {
			changeIDs = append(changeIDs, item.SymbolID)
		}
		if page.NextCursor == "" {
			if pages != 2 {
				t.Fatalf("changes took %d extra pages, want 2", pages)
			}
			break
		}
		changeParams.Page.Cursor = page.NextCursor
	}
	if strings.Join(changeIDs, ",") != "fn-0,fn-1,fn-2,fn-3,fn-4" {
		t.Fatalf("paged changes = %v (review order must be preserved)", changeIDs)
	}

	messages := []string{}
	diagnosticParams := protocol.DiagnosticsListParams{SnapshotQuery: query, Page: &protocol.Page{Limit: 2}}
	for {
		page, err := harness.client.ListDiagnostics(ctx, diagnosticParams)
		if err != nil || page.LoadReport.DiagnosticCount != 3 {
			t.Fatalf("diagnostics page = %#v, %v", page, err)
		}
		for _, item := range page.Items {
			messages = append(messages, item.Message)
		}
		if page.NextCursor == "" {
			break
		}
		diagnosticParams.Page.Cursor = page.NextCursor
	}
	if strings.Join(messages, ",") != "problem 0,problem 1,problem 2" {
		t.Fatalf("paged diagnostics = %v", messages)
	}
}

// TestWireShapeUsesEmptyArraysAndOmitsUnavailableFields verifies the encoding
// rules clients rely on: [] instead of null, and omitted optional fields.
func TestWireShapeUsesEmptyArraysAndOmitsUnavailableFields(t *testing.T) {
	index := fixtureIndex("sample.Root")
	root := index.Functions["root"]
	root.Contracts = []analyzer.Contract{{Name: "sample.Config", Kind: "struct", Fields: []analyzer.Field{{Name: "Root", Type: "string"}}}}
	root.Parameters, root.Results = nil, nil
	index.Functions["root"] = root
	index.Git = analyzer.GitSnapshot{Available: false, Branch: "stale", Revision: "stale"}
	index.LoadReport = analyzer.LoadReport{Language: "go"}

	harness := newHarness(t, Options{Analyze: staticAnalyze(index)})
	ctx := testContext(t)
	viewID := harness.openGo(ctx, nil)
	published := harness.publish(ctx, viewID)
	query := protocol.SnapshotQuery{ViewID: viewID, SnapshotID: published.SnapshotID}

	var symbolResult json.RawMessage
	if err := harness.client.Call(ctx, protocol.MethodSymbolGet, protocol.SymbolGetParams{SnapshotQuery: query, SymbolID: "root"}, &symbolResult); err != nil {
		t.Fatal(err)
	}
	encoded := string(symbolResult)
	for _, expected := range []string{`"parameters":[]`, `"results":[]`, `"evidence":[]`, `"contracts":[{"name":"sample.Config","kind":"struct","fields":[{"name":"Root","type":"string"}],"methods":[]}]`} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("symbol/get omitted %s: %s", expected, encoded)
		}
	}
	if strings.Contains(encoded, "null") {
		t.Fatalf("symbol/get encoded null: %s", encoded)
	}

	var changesResult json.RawMessage
	if err := harness.client.Call(ctx, protocol.MethodChangesList, protocol.ChangesListParams{SnapshotQuery: query}, &changesResult); err != nil {
		t.Fatal(err)
	}
	if got := string(changesResult); got != `{"gitState":{"available":false,"detached":false},"items":[]}` {
		t.Fatalf("unavailable Git encoding = %s", got)
	}

	var diagnosticsResult json.RawMessage
	if err := harness.client.Call(ctx, protocol.MethodDiagnosticsList, protocol.DiagnosticsListParams{SnapshotQuery: query}, &diagnosticsResult); err != nil {
		t.Fatal(err)
	}
	if got := string(diagnosticsResult); !strings.Contains(got, `"buildTags":[]`) || !strings.Contains(got, `"items":[]`) {
		t.Fatalf("empty diagnostics encoding = %s", got)
	}
}

// bulkIndex returns an index with changeCount changed functions in review
// order and diagnosticCount load diagnostics.
func bulkIndex(changeCount int, diagnosticCount int) *analyzer.Index {
	index := &analyzer.Index{
		Root:       testRoot,
		Language:   "go",
		Functions:  map[string]analyzer.Function{},
		Outgoing:   map[string][]analyzer.Edge{},
		Incoming:   map[string][]analyzer.Edge{},
		Git:        analyzer.GitSnapshot{Available: true, Branch: "main"},
		LoadReport: analyzer.LoadReport{Language: "go", TotalUnits: diagnosticCount + 1, FailedUnits: diagnosticCount},
	}
	for number := range changeCount {
		id := fmt.Sprintf("fn-%d", number)
		// Names sort opposite to review order so pagination must keep engine order.
		name := fmt.Sprintf("sample.F%03d", changeCount-number)
		index.Functions[id] = analyzer.Function{ID: id, QualifiedName: name, Package: "sample", File: testRoot + "/bulk.go", Line: number + 1, EndLine: number + 1, Change: &analyzer.FunctionChange{Kind: "new", Diff: "+x\n"}}
		index.Git.ChangedFunctions = append(index.Git.ChangedFunctions, analyzer.ChangedFunction{ID: id, QualifiedName: name, Package: "sample", File: testRoot + "/bulk.go", Line: number + 1, Kind: "new"})
	}
	for number := range diagnosticCount {
		index.LoadReport.Diagnostics = append(index.LoadReport.Diagnostics, analyzer.LoadDiagnostic{Kind: "type", Message: fmt.Sprintf("problem %d", number), Units: []string{fmt.Sprintf("unit-%d", number)}})
	}
	return index
}

// atomicFlag is a small concurrency-safe boolean for test fakes.
type atomicFlag struct {
	mu    sync.Mutex
	value bool
}

func (flag *atomicFlag) get() bool {
	flag.mu.Lock()
	defer flag.mu.Unlock()
	return flag.value
}

func (flag *atomicFlag) set(value bool) {
	flag.mu.Lock()
	defer flag.mu.Unlock()
	flag.value = value
}
