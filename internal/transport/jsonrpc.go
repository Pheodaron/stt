package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"

	"vks-stt-v2/internal/audio"
	"vks-stt-v2/internal/stt"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

type rpcFault struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  *stt.Result     `json:"result,omitempty"`
	Error   *rpcFault       `json:"error,omitempty"`
}

func (w *WebSocket) dispatch(ctx context.Context, session *stt.Session, data []byte) []byte {
	if !json.Valid(data) {
		return rpcError(nil, -32700, "Parse error")
	}

	data = bytes.TrimSpace(data)
	if data[0] == '[' {
		return w.dispatchBatch(ctx, session, data)
	}

	var request rpcRequest
	if data[0] != '{' || json.Unmarshal(data, &request) != nil {
		return rpcError(nil, -32600, "Invalid Request")
	}
	if request.JSONRPC != "2.0" || request.Method == "" || !validID(request.ID) {
		return rpcError(nil, -32600, "Invalid Request")
	}

	result, fault := w.execute(ctx, session, request)
	if request.ID == nil {
		return nil // JSON-RPC notifications execute without a response.
	}
	if fault != nil {
		return rpcError(request.ID, fault.Code, fault.Message)
	}

	response, _ := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  &result,
	})
	return response
}

func (w *WebSocket) dispatchBatch(ctx context.Context, session *stt.Session, data []byte) []byte {
	var batch []json.RawMessage
	if json.Unmarshal(data, &batch) != nil || len(batch) == 0 {
		return rpcError(nil, -32600, "Invalid Request")
	}

	var responses []json.RawMessage
	for _, item := range batch {
		trimmed := bytes.TrimSpace(item)
		if trimmed[0] == '[' {
			// Nested batches are invalid, not recursively executed.
			responses = append(responses, rpcError(nil, -32600, "Invalid Request"))
			continue
		}

		if response := w.dispatch(ctx, session, item); response != nil {
			responses = append(responses, response)
		}
	}

	if len(responses) == 0 {
		return nil
	}

	response, _ := json.Marshal(responses)
	return response
}

func (w *WebSocket) execute(ctx context.Context, session *stt.Session, request rpcRequest) (stt.Result, *rpcFault) {
	switch request.Method {
	case "flush":
		return flushSession(ctx, session, request.Params)
	case "upload_part", "upload_part_unbuffered":
		return w.uploadSegment(ctx, session, request)
	default:
		return stt.Result{}, &rpcFault{Code: -32601, Message: "Method not found"}
	}
}

func flushSession(ctx context.Context, session *stt.Session, params json.RawMessage) (stt.Result, *rpcFault) {
	if !emptyParams(params) {
		return stt.Result{}, &rpcFault{Code: -32602, Message: "Invalid params"}
	}

	result, err := session.Flush(ctx)
	if err != nil {
		return stt.Result{}, recognitionFault(err)
	}
	if result != nil {
		return *result, nil
	}

	empty := ""
	return stt.Result{Message: &empty}, nil
}

func emptyParams(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}

	var params any
	if json.Unmarshal(raw, &params) != nil {
		return false
	}

	switch value := params.(type) {
	case map[string]any:
		return len(value) == 0
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func (w *WebSocket) uploadSegment(ctx context.Context, session *stt.Session, request rpcRequest) (stt.Result, *rpcFault) {
	encoded, ok := segmentParam(request.Params)
	if !ok {
		return stt.Result{}, &rpcFault{Code: -32602, Message: "Invalid params"}
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(w.MaxSegmentBytes) {
		return stt.Result{}, &rpcFault{Code: -32001, Message: "Audio limit exceeded"}
	}

	segment, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return stt.Result{}, &rpcFault{Code: -32602, Message: "Invalid base64 segment"}
	}

	unbuffered := request.Method == "upload_part_unbuffered"
	result, err := session.Transcribe(ctx, segment, unbuffered)
	if err != nil {
		return stt.Result{}, recognitionFault(err)
	}

	return result, nil
}

func recognitionFault(err error) *rpcFault {
	switch {
	case errors.Is(err, audio.ErrAudio):
		return &rpcFault{Code: -32602, Message: "Invalid audio"}
	case errors.Is(err, audio.ErrLimit):
		return &rpcFault{Code: -32001, Message: "Audio limit exceeded"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &rpcFault{Code: -32002, Message: "Request canceled or timed out"}
	default:
		slog.Error("recognition failed", "error", err)
		return &rpcFault{Code: -32603, Message: "Internal error"}
	}
}

func segmentParam(raw json.RawMessage) (string, bool) {
	var params any
	if json.Unmarshal(raw, &params) != nil {
		return "", false
	}

	var value any
	switch p := params.(type) {
	case map[string]any:
		if len(p) != 1 {
			return "", false
		}
		value = p["segment"]
	case []any:
		if len(p) != 1 {
			return "", false
		}
		value = p[0]
	default:
		return "", false
	}
	s, ok := value.(string)
	return s, ok
}

func validID(id json.RawMessage) bool {
	if id == nil || bytes.Equal(id, []byte("null")) {
		return true
	}

	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}

	switch value.(type) {
	case string, float64:
		return true
	}

	return false
}

func rpcError(id json.RawMessage, code int, message string) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	result, _ := json.Marshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcFault{Code: code, Message: message},
	})
	return result
}
