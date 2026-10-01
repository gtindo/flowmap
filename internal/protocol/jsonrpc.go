package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// JSONRPCVersion is the only JSON-RPC envelope version accepted on the wire.
const JSONRPCVersion = "2.0"

// JSON-RPC 2.0 and Flowmap protocol error codes.
const (
	CodeParseError              = -32700
	CodeInvalidRequest          = -32600
	CodeMethodNotFound          = -32601
	CodeInvalidParams           = -32602
	CodeInternalError           = -32603
	CodeProtocolVersionMismatch = -32001
	CodeCapabilityNotSupported  = -32002
	CodeInvalidSessionState     = -32003
	CodeWorkspaceNotFound       = -32004
	CodeViewNotFound            = -32005
	CodeSnapshotUnavailable     = -32006
	CodeAnalysisAlreadyRunning  = -32007
	CodeAnalysisNotFound        = -32008
	CodeSymbolNotFound          = -32009
)

// Message is the union of JSON-RPC requests, notifications, and responses.
// ID is kept raw so string and integer identifiers round-trip verbatim.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// IsRequest reports whether the message expects a response.
func (message Message) IsRequest() bool {
	return message.Method != "" && len(message.ID) > 0
}

// IsNotification reports whether the message is a one-way notification.
func (message Message) IsNotification() bool {
	return message.Method != "" && len(message.ID) == 0
}

// IsResponse reports whether the message answers an earlier request.
func (message Message) IsResponse() bool {
	return message.Method == "" && len(message.ID) > 0
}

// Error is a JSON-RPC error object. Message is human-readable only.
type Error struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// Error implements the error interface.
func (rpcError *Error) Error() string {
	return fmt.Sprintf("%s (code %d)", rpcError.Message, rpcError.Code)
}

// NewError creates a protocol error with optional structured data.
func NewError(code int, message string, data map[string]any) *Error {
	return &Error{Code: code, Message: message, Data: data}
}

// Errorf creates a protocol error without structured data.
func Errorf(code int, format string, arguments ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, arguments...)}
}

// ValidID reports whether a raw identifier is a JSON string or integer.
// Operations (Pure): inspects bytes without side effects.
func ValidID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}

	if trimmed[0] == '"' {
		var text string
		return json.Unmarshal(trimmed, &text) == nil
	}

	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return false
	}

	_, err := number.Int64()
	return err == nil
}
