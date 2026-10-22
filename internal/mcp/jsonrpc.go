// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
)

// JSON-RPC 2.0 and MCP error codes. -32020…-32022 are the MCP 2026-07-28
// protocol errors; the rest are the standard JSON-RPC ones.
const (
	codeParseError         = -32700
	codeInvalidRequest     = -32600
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeInternalError      = -32603
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
	codeResourceNotFound   = -32002 // legacy revisions only; 2026-07-28 uses -32602
)

// message is one decoded JSON-RPC object as sent by the client. ID keeps the
// raw token so it is echoed byte for byte; it is nil when absent
// (notification) and "null" when explicitly null (invalid in MCP).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  json.RawMessage `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`

	method string // decoded Method
}

func (m *message) isNotification() bool { return m.ID == nil && m.method != "" }
func (m *message) isResponse() bool     { return m.method == "" && (m.Result != nil || m.Error != nil) }

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

func newError(code int, msg string) *rpcError { return &rpcError{Code: code, Message: msg} }

// response is a JSON-RPC response; exactly one of Result and Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

func resultResponse(id json.RawMessage, result any) *response {
	return &response{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, err *rpcError) *response {
	if id == nil {
		id = nullID
	}
	return &response{JSONRPC: "2.0", ID: id, Error: err}
}

// decodeMessage validates one JSON-RPC object. An invalid object yields an
// Invalid Request error carrying whatever id could be read.
func decodeMessage(raw json.RawMessage) (*message, *response) {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: not a JSON-RPC object"))
	}
	id := m.ID
	if id != nil && !validID(id) {
		return nil, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: id must be a string or an integer"))
	}
	if m.JSONRPC != "2.0" {
		return nil, errorResponse(id, newError(codeInvalidRequest, `Invalid Request: jsonrpc must be "2.0"`))
	}
	if m.Method != nil {
		if err := json.Unmarshal(m.Method, &m.method); err != nil || m.method == "" {
			return nil, errorResponse(id, newError(codeInvalidRequest, "Invalid Request: method must be a non-empty string"))
		}
	}
	switch {
	case m.method == "" && m.Result == nil && m.Error == nil:
		return nil, errorResponse(id, newError(codeInvalidRequest, "Invalid Request: method is required"))
	case m.method == "" && id == nil:
		return nil, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: a response needs an id"))
	}
	if m.Params != nil && !bytes.HasPrefix(bytes.TrimSpace(m.Params), []byte("{")) {
		return nil, errorResponse(id, newError(codeInvalidRequest, "Invalid Request: params must be an object"))
	}
	return &m, nil
}

// validID: MCP request ids are strings or integers, never null.
func validID(id json.RawMessage) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(id))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return true
	case json.Number:
		_, err := x.Int64()
		return err == nil
	}
	return false
}

// splitBatch reports whether body is a JSON array and returns its elements.
func splitBatch(body []byte) (items []json.RawMessage, batch bool, err error) {
	t := bytes.TrimSpace(body)
	if len(t) == 0 {
		return nil, false, errors.New("empty body")
	}
	if t[0] != '[' {
		if !json.Valid(t) {
			return nil, false, errors.New("invalid JSON")
		}
		return []json.RawMessage{t}, false, nil
	}
	if err := json.Unmarshal(t, &items); err != nil {
		return nil, true, err
	}
	return items, true, nil
}

// decodeParams unmarshals params (absent = {}) into dst.
func decodeParams(params json.RawMessage, dst any) *rpcError {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, dst); err != nil {
		return newError(codeInvalidParams, "Invalid params: "+err.Error())
	}
	return nil
}
