package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// ErrClientClosed reports a call on a client whose stream has ended.
var ErrClientClosed = errors.New("protocol client closed")

// NotificationHandler receives engine notifications in arrival order. It runs
// on the client's read loop, so it must not block on further client calls.
type NotificationHandler func(method string, params json.RawMessage)

// Client is a JSON-RPC client for the Flowmap engine protocol. It correlates
// responses by identifier, so callers may issue concurrent requests.
type Client struct {
	writer *FrameWriter
	closer io.Closer

	mu       sync.Mutex
	nextID   int64
	pending  map[string]chan Message
	handlers []NotificationHandler
	session  InitializeResult
	readErr  error

	done chan struct{}
}

// NewClient starts reading engine messages from reader and writes requests to
// writer. Closing the client closes writer, which the engine observes as EOF.
// Side Effect (Edge): starts a goroutine that owns reader.
func NewClient(reader io.Reader, writer io.WriteCloser) *Client {
	client := &Client{
		writer:  NewFrameWriter(writer),
		closer:  writer,
		pending: make(map[string]chan Message),
		done:    make(chan struct{}),
	}

	go client.readLoop(NewFrameReader(reader))
	return client
}

// OnNotification registers a handler for every subsequent notification.
func (client *Client) OnNotification(handler NotificationHandler) {
	client.mu.Lock()
	defer client.mu.Unlock()

	client.handlers = append(client.handlers, handler)
}

// Done is closed when the engine stream ends.
func (client *Client) Done() <-chan struct{} {
	return client.done
}

// Err returns the reason the engine stream ended, if it has.
func (client *Client) Err() error {
	client.mu.Lock()
	defer client.mu.Unlock()

	return client.readErr
}

// Close closes the outbound stream without a protocol shutdown.
func (client *Client) Close() error {
	return client.closer.Close()
}

// Session returns the result of a successful Initialize call.
func (client *Client) Session() InitializeResult {
	client.mu.Lock()
	defer client.mu.Unlock()

	return client.session
}

// Call sends one request and decodes its result into result when non-nil.
// Engine errors are returned as *Error.
// Side Effect (Edge): writes to and waits on the engine stream.
func (client *Client) Call(ctx context.Context, method string, params any, result any) error {
	id, responses, err := client.register()
	if err != nil {
		return err
	}
	defer client.unregister(id)

	request, err := encodeMessage(Message{JSONRPC: JSONRPCVersion, ID: json.RawMessage(id), Method: method}, params)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	if err := client.writer.Write(request); err != nil {
		return fmt.Errorf("send %s request: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case response, ok := <-responses:
		if !ok {
			return fmt.Errorf("%s: %w", method, client.closedError())
		}
		if response.Error != nil {
			return response.Error
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

// Notify sends one notification.
// Side Effect (Edge): writes to the engine stream.
func (client *Client) Notify(method string, params any) error {
	notification, err := encodeMessage(Message{JSONRPC: JSONRPCVersion, Method: method}, params)
	if err != nil {
		return fmt.Errorf("encode %s notification: %w", method, err)
	}
	if err := client.writer.Write(notification); err != nil {
		return fmt.Errorf("send %s notification: %w", method, err)
	}
	return nil
}

// Initialize negotiates protocol version 0 and remembers the session result.
func (client *Client) Initialize(ctx context.Context, params InitializeParams) (InitializeResult, error) {
	if params.ProtocolVersion == "" {
		params.ProtocolVersion = Version
	}

	var result InitializeResult
	if err := client.Call(ctx, MethodInitialize, params, &result); err != nil {
		return InitializeResult{}, err
	}

	client.mu.Lock()
	client.session = result
	client.mu.Unlock()
	return result, nil
}

// Shutdown asks the engine to stop work, then sends exit.
func (client *Client) Shutdown(ctx context.Context) error {
	if err := client.Call(ctx, MethodShutdown, nil, nil); err != nil {
		return err
	}
	return client.Notify(MethodExit, nil)
}

// OpenWorkspace registers one repository root.
func (client *Client) OpenWorkspace(ctx context.Context, params WorkspaceOpenParams) (Workspace, error) {
	var result Workspace
	err := client.Call(ctx, MethodWorkspaceOpen, params, &result)
	return result, err
}

// GetWorkspace returns a workspace and the current load state of its views.
func (client *Client) GetWorkspace(ctx context.Context, workspaceID string) (Workspace, error) {
	var result Workspace
	err := client.Call(ctx, MethodWorkspaceGet, WorkspaceGetParams{WorkspaceID: workspaceID}, &result)
	return result, err
}

// CloseWorkspace closes a workspace and evicts its snapshots.
func (client *Client) CloseWorkspace(ctx context.Context, workspaceID string) error {
	return client.Call(ctx, MethodWorkspaceClose, WorkspaceCloseParams{WorkspaceID: workspaceID}, nil)
}

// StartAnalysis starts analysis of one view and returns once it is accepted.
func (client *Client) StartAnalysis(ctx context.Context, params AnalysisStartParams) (string, error) {
	var result AnalysisStartResult
	err := client.Call(ctx, MethodAnalysisStart, params, &result)
	return result.AnalysisID, err
}

// CancelAnalysis requests best-effort cancellation.
func (client *Client) CancelAnalysis(ctx context.Context, analysisID string) (bool, error) {
	var result AnalysisCancelResult
	err := client.Call(ctx, MethodAnalysisCancel, AnalysisCancelParams{AnalysisID: analysisID}, &result)
	return result.CancelRequested, err
}

// SearchSymbols returns one page of matching symbols.
func (client *Client) SearchSymbols(ctx context.Context, params SymbolSearchParams) (SymbolSearchResult, error) {
	var result SymbolSearchResult
	err := client.Call(ctx, MethodSymbolSearch, params, &result)
	return result, err
}

// GetSymbol returns one full symbol.
func (client *Client) GetSymbol(ctx context.Context, params SymbolGetParams) (Symbol, error) {
	var result SymbolGetResult
	err := client.Call(ctx, MethodSymbolGet, params, &result)
	return result.Symbol, err
}

// Neighborhood returns a bounded graph neighborhood.
func (client *Client) Neighborhood(ctx context.Context, params GraphNeighborhoodParams) (Graph, error) {
	var result Graph
	err := client.Call(ctx, MethodGraphNeighborhood, params, &result)
	return result, err
}

// ListChanges returns one page of Git-changed symbols.
func (client *Client) ListChanges(ctx context.Context, params ChangesListParams) (ChangesListResult, error) {
	var result ChangesListResult
	err := client.Call(ctx, MethodChangesList, params, &result)
	return result, err
}

// ListDiagnostics returns one page of load diagnostics.
func (client *Client) ListDiagnostics(ctx context.Context, params DiagnosticsListParams) (DiagnosticsListResult, error) {
	var result DiagnosticsListResult
	err := client.Call(ctx, MethodDiagnosticsList, params, &result)
	return result, err
}

// SummarizeSymbol returns a generated or cached summary.
func (client *Client) SummarizeSymbol(ctx context.Context, params SymbolSummaryParams) (SymbolSummaryResult, error) {
	var result SymbolSummaryResult
	err := client.Call(ctx, MethodSymbolSummary, params, &result)
	return result, err
}

func (client *Client) register() (string, chan Message, error) {
	client.mu.Lock()
	defer client.mu.Unlock()

	select {
	case <-client.done:
		return "", nil, client.closedErrorLocked()
	default:
	}

	client.nextID++
	id := strconv.FormatInt(client.nextID, 10)
	responses := make(chan Message, 1)
	client.pending[id] = responses
	return id, responses, nil
}

func (client *Client) unregister(id string) {
	client.mu.Lock()
	defer client.mu.Unlock()

	delete(client.pending, id)
}

func (client *Client) readLoop(reader *FrameReader) {
	var finalErr error
	defer client.finish(&finalErr)

	for {
		payload, err := reader.Read()
		if err != nil {
			finalErr = err
			return
		}

		var message Message
		if err := json.Unmarshal(payload, &message); err != nil {
			// A malformed engine message cannot be correlated; skip it.
			continue
		}

		switch {
		case message.IsResponse():
			client.deliver(message)
		case message.IsNotification():
			client.notify(message)
		}
	}
}

func (client *Client) deliver(message Message) {
	client.mu.Lock()
	responses := client.pending[string(message.ID)]
	delete(client.pending, string(message.ID))
	client.mu.Unlock()

	if responses != nil {
		responses <- message
	}
}

func (client *Client) notify(message Message) {
	client.mu.Lock()
	handlers := append([]NotificationHandler(nil), client.handlers...)
	client.mu.Unlock()

	for _, handler := range handlers {
		handler(message.Method, message.Params)
	}
}

func (client *Client) finish(finalErr *error) {
	client.mu.Lock()
	defer client.mu.Unlock()

	if errors.Is(*finalErr, io.EOF) {
		*finalErr = nil
	}
	client.readErr = *finalErr
	for id, responses := range client.pending {
		close(responses)
		delete(client.pending, id)
	}
	close(client.done)
}

func (client *Client) closedError() error {
	client.mu.Lock()
	defer client.mu.Unlock()

	return client.closedErrorLocked()
}

func (client *Client) closedErrorLocked() error {
	if client.readErr != nil {
		return fmt.Errorf("%w: %w", ErrClientClosed, client.readErr)
	}
	return ErrClientClosed
}

// encodeMessage attaches params to an envelope, omitting nil params.
// Operations (Pure): JSON encoding only.
func encodeMessage(message Message, params any) ([]byte, error) {
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		message.Params = encoded
	}
	return json.Marshal(message)
}
