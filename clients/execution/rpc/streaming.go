package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// jsonRPCRequest represents a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

// jsonRPCError represents a JSON-RPC 2.0 error.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("RPC error (code %d): %s", e.Code, e.Message)
}

// ResponseDecodeError marks a failure that happened while decoding a response
// that was received in full. The shape of the payload rather than the
// connection is the problem, so repeating the same call against another client
// yields the same failure.
type ResponseDecodeError struct {
	Err error
}

func (e *ResponseDecodeError) Error() string {
	return e.Err.Error()
}

func (e *ResponseDecodeError) Unwrap() error {
	return e.Err
}

// asDecodeError classifies a decoding failure. Structural JSON problems become
// a ResponseDecodeError, while I/O and context failures pass through unchanged
// so callers can still fail over to another client.
func asDecodeError(err error) error {
	if err == nil {
		return nil
	}

	var (
		decodeErr *ResponseDecodeError
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
	)

	switch {
	case errors.As(err, &decodeErr):
		return err
	case errors.As(err, &syntaxErr), errors.As(err, &typeErr):
		return &ResponseDecodeError{Err: err}
	default:
		return err
	}
}

// streamRPCCall makes a JSON-RPC call and stream-decodes the "result"
// field by passing a *json.Decoder to decodeFn. For HTTP endpoints, the
// response body is streamed directly from the network, avoiding the
// intermediate json.RawMessage buffering that go-ethereum's rpc.Client
// uses internally. For non-HTTP endpoints, it falls back to
// rpc.Client.CallContext with json.RawMessage.
//
// This significantly reduces peak memory for large responses such as
// debug_traceBlockByHash, which can return hundreds of megabytes of JSON.
// Instead of holding both the raw JSON bytes and the decoded Go structs
// simultaneously, only the decoded structs are kept in memory.
func (ec *ExecutionClient) streamRPCCall(
	ctx context.Context,
	method string,
	decodeFn func(decoder *json.Decoder) error,
	args ...any,
) error {
	if strings.HasPrefix(ec.endpoint, "http://") ||
		strings.HasPrefix(ec.endpoint, "https://") {
		return ec.streamRPCCallHTTP(ctx, method, decodeFn, args...)
	}

	// Fallback for non-HTTP endpoints (WebSocket, IPC): use standard
	// rpc.Client with json.RawMessage. Still buffers the result, but
	// the caller code is identical.
	var raw json.RawMessage
	if err := ec.rpcClient.CallContext(ctx, &raw, method, args...); err != nil {
		return err
	}

	return decodeFn(json.NewDecoder(bytes.NewReader(raw)))
}

// streamRPCCallHTTP makes a raw HTTP JSON-RPC call and stream-decodes
// the result field directly from the response body.
func (ec *ExecutionClient) streamRPCCallHTTP(
	ctx context.Context,
	method string,
	decodeFn func(decoder *json.Decoder) error,
	args ...any,
) error {
	reqBody, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  args,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, ec.endpoint, bytes.NewReader(reqBody),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range ec.headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	return decodeStreamedJSONRPCResult(resp.Body, decodeFn)
}

// decodeStreamedJSONRPCResult parses a JSON-RPC envelope from r and
// calls decodeFn when the "result" field is encountered. If the response
// contains an "error" field, it returns the error without calling decodeFn.
func decodeStreamedJSONRPCResult(
	r io.Reader,
	decodeFn func(*json.Decoder) error,
) error {
	dec := json.NewDecoder(r)

	// Read opening '{' of JSON-RPC envelope
	t, err := dec.Token()
	if err != nil {
		return asDecodeError(fmt.Errorf("read response: %w", err))
	}

	if t != json.Delim('{') {
		return &ResponseDecodeError{Err: fmt.Errorf("expected '{', got %v", t)}
	}

	var foundResult bool

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return asDecodeError(fmt.Errorf("read key: %w", err))
		}

		key, ok := keyTok.(string)
		if !ok {
			return &ResponseDecodeError{Err: fmt.Errorf("expected string key, got %T", keyTok)}
		}

		switch key {
		case "error":
			var rpcErr jsonRPCError
			if err := dec.Decode(&rpcErr); err != nil {
				return asDecodeError(fmt.Errorf("decode error field: %w", err))
			}

			return &rpcErr

		case "result":
			if err := decodeFn(dec); err != nil {
				return err
			}

			foundResult = true

		default:
			// Skip other fields (jsonrpc, id) without materialising them.
			if err := skipJSONValue(dec); err != nil {
				return fmt.Errorf("skip field %q: %w", key, err)
			}
		}
	}

	if !foundResult {
		return &ResponseDecodeError{Err: fmt.Errorf("no 'result' field in JSON-RPC response")}
	}

	return nil
}

// streamDecodeArray returns a decodeFn for streamRPCCall that
// stream-decodes a JSON array one element at a time, appending each
// to *results, so the raw JSON of the whole array never has to be held
// at once.
//
// The decoder still buffers each element in full before it is handed over.
// Where a single element can be arbitrarily large - a call trace, for
// instance - decode it field by field instead, as decodeCallTraceResults does.
func streamDecodeArray[T any](results *[]T) func(*json.Decoder) error {
	return func(dec *json.Decoder) error {
		t, err := dec.Token()
		if err != nil {
			return asDecodeError(fmt.Errorf("read array start: %w", err))
		}

		// Handle null result
		if t == nil {
			return nil
		}

		if t != json.Delim('[') {
			return &ResponseDecodeError{Err: fmt.Errorf("expected '[', got %v", t)}
		}

		for dec.More() {
			var elem T
			if err := dec.Decode(&elem); err != nil {
				return asDecodeError(fmt.Errorf("decode element: %w", err))
			}

			*results = append(*results, elem)
		}

		// Read closing ']'
		if _, err := dec.Token(); err != nil {
			return asDecodeError(fmt.Errorf("read array end: %w", err))
		}

		return nil
	}
}
