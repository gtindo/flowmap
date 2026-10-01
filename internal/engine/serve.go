package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gtindo/flowmap/internal/protocol"
)

// ErrExitWithoutShutdown reports that the session ended before a completed
// shutdown request; process shells should exit with a non-zero status.
var ErrExitWithoutShutdown = errors.New("engine exited without shutdown")

// handlerResult is a method outcome. after runs once the response is written.
type handlerResult struct {
	value any
	after func()
	err   *protocol.Error
}

type methodHandler func(ctx context.Context, params json.RawMessage) handlerResult

// Serve runs the session until exit, end of input, ctx cancellation, or an
// unrecoverable framing error. Responses may be written out of request order.
// Side Effect (Edge): reads input, writes output, and runs analyses.
func (engine *Engine) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	engine.ctx, engine.cancel = context.WithCancel(ctx)
	defer engine.cancel()

	engine.writer = protocol.NewFrameWriter(output)
	defer engine.stop()

	frames := make(chan []byte)
	readErrors := make(chan error, 1)
	go readFrames(engine.ctx, protocol.NewFrameReader(input), frames, readErrors)

	for {
		select {
		case <-engine.ctx.Done():
			return ctx.Err()
		case err := <-readErrors:
			if errors.Is(err, io.EOF) {
				// End of input is equivalent to exit.
				return engine.exitStatus()
			}
			engine.logger.Error("protocol stream terminated", "error", err)
			return fmt.Errorf("read protocol stream: %w", err)
		case payload := <-frames:
			if exited := engine.dispatch(payload); exited {
				return engine.exitStatus()
			}
		}
	}
}

func readFrames(ctx context.Context, reader *protocol.FrameReader, frames chan<- []byte, readErrors chan<- error) {
	for {
		payload, err := reader.Read()
		if err != nil {
			readErrors <- err
			return
		}

		select {
		case frames <- payload:
		case <-ctx.Done():
			return
		}
	}
}

// stop cancels remaining work, waits for request handlers, and silences
// late analysis notifications.
func (engine *Engine) stop() {
	engine.cancel()
	engine.handlers.Wait()

	engine.mu.Lock()
	engine.closed = true
	engine.mu.Unlock()
}

func (engine *Engine) exitStatus() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	if engine.state == sessionShutdown {
		return nil
	}
	return ErrExitWithoutShutdown
}

// dispatch handles one framed payload and reports whether exit was received.
func (engine *Engine) dispatch(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if !json.Valid(trimmed) {
		engine.respond(nil, nil, protocol.Errorf(protocol.CodeParseError, "invalid JSON payload"))
		return false
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		engine.respond(nil, nil, protocol.Errorf(protocol.CodeInvalidRequest, "a single JSON-RPC request object is required; batches are not supported"))
		return false
	}

	var message protocol.Message
	if err := json.Unmarshal(trimmed, &message); err != nil {
		engine.respond(nil, nil, protocol.Errorf(protocol.CodeInvalidRequest, "invalid JSON-RPC message: %v", err))
		return false
	}

	hasID := len(message.ID) > 0
	if hasID && !protocol.ValidID(message.ID) {
		engine.respond(nil, nil, protocol.Errorf(protocol.CodeInvalidRequest, "id must be a string or integer"))
		return false
	}
	if message.JSONRPC != protocol.JSONRPCVersion {
		if hasID {
			engine.respond(message.ID, nil, protocol.Errorf(protocol.CodeInvalidRequest, "jsonrpc must be %q", protocol.JSONRPCVersion))
		}
		return false
	}

	switch {
	case message.IsNotification():
		return message.Method == protocol.MethodExit
	case message.IsRequest():
		engine.route(message)
	}
	return false
}

// route applies lifecycle rules. initialize and shutdown run on the read loop
// so they are ordered with respect to every later request.
func (engine *Engine) route(message protocol.Message) {
	engine.mu.Lock()
	state := engine.state
	engine.mu.Unlock()

	switch message.Method {
	case protocol.MethodInitialize:
		if state != sessionUninitialized {
			engine.respond(message.ID, nil, invalidState("initialize is valid exactly once"))
			return
		}
		engine.respondResult(message.ID, engine.handleInitialize(message.Params))
		return
	case protocol.MethodShutdown:
		if state != sessionInitialized {
			engine.respond(message.ID, nil, invalidState("shutdown requires an initialized session"))
			return
		}
		engine.respondResult(message.ID, engine.handleShutdown())
		return
	}

	handler := engine.methods()[message.Method]
	if handler == nil {
		engine.respond(message.ID, nil, protocol.NewError(protocol.CodeMethodNotFound, "method not found", map[string]any{"method": message.Method}))
		return
	}
	if state != sessionInitialized {
		engine.respond(message.ID, nil, invalidState("the session is not accepting requests"))
		return
	}

	engine.handlers.Add(1)
	go func() {
		defer engine.handlers.Done()
		engine.respondResult(message.ID, engine.invoke(handler, message))
	}()
}

func (engine *Engine) invoke(handler methodHandler, message protocol.Message) (result handlerResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			engine.logger.Error("request handler panicked", "method", message.Method, "panic", recovered)
			result = handlerResult{err: protocol.Errorf(protocol.CodeInternalError, "internal error handling %s", message.Method)}
		}
	}()

	return handler(engine.ctx, message.Params)
}

func invalidState(message string) *protocol.Error {
	return protocol.Errorf(protocol.CodeInvalidSessionState, "%s", message)
}

func (engine *Engine) respondResult(id json.RawMessage, result handlerResult) {
	engine.respond(id, result.value, result.err)
	if result.err == nil && result.after != nil {
		result.after()
	}
}

// respond writes one response; a nil id encodes as JSON null.
func (engine *Engine) respond(id json.RawMessage, value any, rpcError *protocol.Error) {
	if id == nil {
		id = json.RawMessage("null")
	}

	response := protocol.Message{JSONRPC: protocol.JSONRPCVersion, ID: id, Error: rpcError}
	if rpcError == nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			engine.logger.Error("encode response", "error", err)
			response.Error = protocol.Errorf(protocol.CodeInternalError, "failed to encode result")
		} else {
			response.Result = encoded
		}
	}

	engine.write(response)
}

// notify writes one notification unless the session has already stopped.
func (engine *Engine) notify(method string, params any) {
	engine.mu.Lock()
	closed := engine.closed
	engine.mu.Unlock()
	if closed {
		return
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		engine.logger.Error("encode notification", "method", method, "error", err)
		return
	}
	engine.write(protocol.Message{JSONRPC: protocol.JSONRPCVersion, Method: method, Params: encoded})
}

func (engine *Engine) write(message protocol.Message) {
	encoded, err := json.Marshal(message)
	if err != nil {
		engine.logger.Error("encode message", "error", err)
		return
	}
	if err := engine.writer.Write(encoded); err != nil {
		engine.logger.Warn("write protocol message", "error", err)
	}
}

// decodeParams decodes required params; unknown fields are ignored.
func decodeParams(raw json.RawMessage, target any) *protocol.Error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return protocol.Errorf(protocol.CodeInvalidParams, "params are required")
	}
	if err := json.Unmarshal(trimmed, target); err != nil {
		return protocol.Errorf(protocol.CodeInvalidParams, "invalid params: %v", err)
	}
	return nil
}

func (engine *Engine) handleInitialize(raw json.RawMessage) handlerResult {
	var params protocol.InitializeParams
	if err := decodeParams(raw, &params); err != nil {
		return handlerResult{err: err}
	}

	result, err := engine.initialize(params)
	return handlerResult{value: result, err: err}
}

func (engine *Engine) handleShutdown() handlerResult {
	engine.beginShutdown()
	engine.handlers.Wait()
	engine.finishShutdown()
	return handlerResult{value: nil}
}
