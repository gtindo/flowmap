package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

const clientTestTimeout = 5 * time.Second

// fakeEngine exposes the engine side of a client connection.
type fakeEngine struct {
	reader *FrameReader
	writer *FrameWriter
	output *io.PipeWriter
}

func newClientPair(t *testing.T) (*Client, *fakeEngine) {
	t.Helper()

	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	client := NewClient(responseReader, requestWriter)
	t.Cleanup(func() {
		_ = client.Close()
		_ = responseWriter.Close()
	})

	return client, &fakeEngine{reader: NewFrameReader(requestReader), writer: NewFrameWriter(responseWriter), output: responseWriter}
}

func (engine *fakeEngine) readRequest(t *testing.T) Message {
	t.Helper()

	payload, err := engine.reader.Read()
	if err != nil {
		t.Errorf("read request: %v", err)
		return Message{}
	}
	var message Message
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return message
}

func (engine *fakeEngine) reply(t *testing.T, id json.RawMessage, result any) {
	t.Helper()

	encoded, _ := json.Marshal(result)
	payload, _ := json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: id, Result: encoded})
	if err := engine.writer.Write(payload); err != nil {
		t.Errorf("write response: %v", err)
	}
}

// TestClientCorrelatesConcurrentOutOfOrderResponses verifies responses reach
// their own callers when the engine answers in reverse order.
func TestClientCorrelatesConcurrentOutOfOrderResponses(t *testing.T) {
	client, engine := newClientPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), clientTestTimeout)
	defer cancel()

	const callCount = 8
	results := make([]string, callCount)
	errs := make([]error, callCount)
	var calls sync.WaitGroup
	for index := range callCount {
		calls.Add(1)
		go func() {
			defer calls.Done()
			var result struct {
				Echo string `json:"echo"`
			}
			errs[index] = client.Call(ctx, "test/echo", map[string]string{"value": fmt.Sprint(index)}, &result)
			results[index] = result.Echo
		}()
	}

	requests := make([]Message, 0, callCount)
	for range callCount {
		requests = append(requests, engine.readRequest(t))
	}
	for index := len(requests) - 1; index >= 0; index-- {
		var params map[string]string
		_ = json.Unmarshal(requests[index].Params, &params)
		engine.reply(t, requests[index].ID, map[string]string{"echo": params["value"]})
	}
	calls.Wait()

	for index := range callCount {
		if errs[index] != nil || results[index] != fmt.Sprint(index) {
			t.Fatalf("call %d = %q, %v", index, results[index], errs[index])
		}
	}
}

// TestClientDeliversNotificationsAndErrors verifies notification ordering and
// that engine errors surface as *Error values.
func TestClientDeliversNotificationsAndErrors(t *testing.T) {
	client, engine := newClientPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), clientTestTimeout)
	defer cancel()

	received := make(chan string, 2)
	client.OnNotification(func(method string, _ json.RawMessage) { received <- method })

	go func() {
		request := engine.readRequest(t)
		for _, method := range []string{MethodAnalysisProgress, MethodAnalysisPublished} {
			payload, _ := json.Marshal(Message{JSONRPC: JSONRPCVersion, Method: method, Params: json.RawMessage(`{}`)})
			_ = engine.writer.Write(payload)
		}
		payload, _ := json.Marshal(Message{JSONRPC: JSONRPCVersion, ID: request.ID, Error: NewError(CodeSymbolNotFound, "symbol not found", nil)})
		_ = engine.writer.Write(payload)
	}()

	err := client.Call(ctx, MethodSymbolGet, SymbolGetParams{SymbolID: "x"}, nil)
	var rpcError *Error
	if !errors.As(err, &rpcError) || rpcError.Code != CodeSymbolNotFound {
		t.Fatalf("Call() error = %v", err)
	}
	if first, second := <-received, <-received; first != MethodAnalysisProgress || second != MethodAnalysisPublished {
		t.Fatalf("notification order = %s, %s", first, second)
	}
}

// TestClientFailsPendingCallsWhenEngineStops verifies a crashed engine never
// leaves callers waiting.
func TestClientFailsPendingCallsWhenEngineStops(t *testing.T) {
	client, engine := newClientPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), clientTestTimeout)
	defer cancel()

	pending := make(chan error, 1)
	go func() { pending <- client.Call(ctx, MethodSymbolSearch, SymbolSearchParams{}, nil) }()

	engine.readRequest(t)
	if _, err := io.WriteString(engine.output, "Content-Length: nope\r\n\r\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-pending:
		if !errors.Is(err, ErrClientClosed) || !errors.Is(err, ErrFraming) {
			t.Fatalf("pending call error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("pending call was not released when the engine stream failed")
	}

	<-client.Done()
	if !errors.Is(client.Err(), ErrFraming) {
		t.Fatalf("Err() = %v", client.Err())
	}
	if err := client.Call(ctx, MethodSymbolSearch, SymbolSearchParams{}, nil); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("call after close = %v", err)
	}
}

// TestClientCallHonorsContext verifies an unanswered call returns on cancellation.
func TestClientCallHonorsContext(t *testing.T) {
	client, engine := newClientPair(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- client.Call(ctx, MethodSymbolSearch, SymbolSearchParams{}, nil) }()
	engine.readRequest(t)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Call() = %v", err)
		}
	case <-time.After(clientTestTimeout):
		t.Fatal("call ignored context cancellation")
	}
}
