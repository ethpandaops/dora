package rpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// decodeCallTraceResults returns a decodeFn for streamRPCCall that walks the
// callTracer result array token by token, appending one CallTraceResult per
// transaction to *results.
//
// Decoding the array with json.Decoder.Decode, as streamDecodeArray does,
// makes the decoder buffer one whole transaction's trace before it hands the
// value over. A contract that loops over calls carrying large memory buffers
// turns a single transaction into hundreds of megabytes of tracer output, all
// of which would then have to sit in the decoder buffer at once. Walking the
// tree scalar by scalar bounds that buffer by the largest single value in the
// response instead, and payloadLimit bounds what is retained from each of
// those values.
func decodeCallTraceResults(results *[]CallTraceResult, payloadLimit int) func(*json.Decoder) error {
	return func(dec *json.Decoder) error {
		tok, err := dec.Token()
		if err != nil {
			return asDecodeError(fmt.Errorf("read trace array start: %w", err))
		}

		// Handle null result
		if tok == nil {
			return nil
		}

		if tok != json.Delim('[') {
			return &ResponseDecodeError{Err: fmt.Errorf("expected '[', got %v", tok)}
		}

		for dec.More() {
			var result CallTraceResult

			if err := decodeCallTraceResult(dec, &result, payloadLimit); err != nil {
				return err
			}

			*results = append(*results, result)
		}

		if _, err := dec.Token(); err != nil {
			return asDecodeError(fmt.Errorf("read trace array end: %w", err))
		}

		return nil
	}
}

// decodeCallTraceResult decodes one per-transaction entry of the callTracer
// result array.
func decodeCallTraceResult(dec *json.Decoder, result *CallTraceResult, payloadLimit int) error {
	_, err := decodeJSONObject(dec, "trace result", func(key string) error {
		switch key {
		case "txHash":
			return decodeScalar(dec, key, &result.TxHash)

		case "result":
			roots, err := decodeCallFrameRoots(dec, payloadLimit)
			if err != nil {
				return err
			}

			result.Roots = roots

			return nil

		default:
			return skipJSONValue(dec)
		}
	})

	return err
}

// decodeCallFrame decodes one callTracer frame and the subtree below it,
// pruning the input and output payloads to payloadLimit as they are read.
// Returns nil when the frame is JSON null.
func decodeCallFrame(dec *json.Decoder, payloadLimit int) (*CallTraceCall, error) {
	call := &CallTraceCall{}

	present, err := decodeJSONObject(dec, "call frame", callFrameField(dec, call, payloadLimit))
	if err != nil {
		return nil, err
	}

	if !present {
		return nil, nil
	}

	return call, nil
}

// decodeCallFrameBody decodes a call frame whose opening brace has already been read.
func decodeCallFrameBody(dec *json.Decoder, payloadLimit int) (*CallTraceCall, error) {
	call := &CallTraceCall{}

	if err := decodeJSONObjectBody(dec, "call frame", callFrameField(dec, call, payloadLimit)); err != nil {
		return nil, err
	}

	return call, nil
}

// decodeCallFrameRoots decodes a transaction's trace result: either one root call frame,
// or a list of them for a transaction whose client decomposes it into several.
func decodeCallFrameRoots(dec *json.Decoder, payloadLimit int) ([]*CallTraceCall, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, asDecodeError(fmt.Errorf("read trace result start: %w", err))
	}

	switch tok {
	case nil:
		return nil, nil

	case json.Delim('{'):
		call, err := decodeCallFrameBody(dec, payloadLimit)
		if err != nil {
			return nil, err
		}

		return []*CallTraceCall{call}, nil

	case json.Delim('['):
		roots := make([]*CallTraceCall, 0, 4)

		for dec.More() {
			call, err := decodeCallFrame(dec, payloadLimit)
			if err != nil {
				return nil, err
			}

			if call != nil {
				roots = append(roots, call)
			}
		}

		if _, err := dec.Token(); err != nil {
			return nil, asDecodeError(fmt.Errorf("read trace result end: %w", err))
		}

		return roots, nil

	default:
		return nil, &ResponseDecodeError{
			Err: fmt.Errorf("expected '{' or '[' for trace result, got %v", tok),
		}
	}
}

// callFrameField returns the field decoder for one call frame's members.
func callFrameField(dec *json.Decoder, call *CallTraceCall, payloadLimit int) func(key string) error {
	return func(key string) error {
		switch key {
		case "type":
			return decodeScalar(dec, key, &call.Type)
		case "from":
			return decodeScalar(dec, key, &call.From)
		case "to":
			return decodeScalar(dec, key, &call.To)
		case "value":
			return decodeScalar(dec, key, &call.Value)
		case "gas":
			return decodeScalar(dec, key, &call.Gas)
		case "gasUsed":
			return decodeScalar(dec, key, &call.GasUsed)
		case "regularGasUsed":
			return decodeScalar(dec, key, &call.RegularGasUsed)
		case "stateGasUsed":
			return decodeScalar(dec, key, &call.StateGasUsed)
		case "gasRefund":
			return decodeScalar(dec, key, &call.GasRefund)
		case "error":
			return decodeScalar(dec, key, &call.Error)

		case "input":
			return decodePrunedPayload(dec, key, payloadLimit, false, (*[]byte)(&call.Input))
		case "output":
			return decodePrunedPayload(dec, key, payloadLimit, false, (*[]byte)(&call.Output))
		case "revertReason":
			return decodePrunedPayload(dec, key, payloadLimit, true, (*[]byte)(&call.RevertReason))

		case "calls":
			calls, err := decodeCallFrames(dec, payloadLimit)
			if err != nil {
				return err
			}

			call.Calls = calls

			return nil

		default:
			return skipJSONValue(dec)
		}
	}
}

// decodeCallFrames decodes the nested "calls" array of a call frame.
func decodeCallFrames(dec *json.Decoder, payloadLimit int) ([]CallTraceCall, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, asDecodeError(fmt.Errorf("read calls array start: %w", err))
	}

	if tok == nil {
		return nil, nil
	}

	if tok != json.Delim('[') {
		return nil, &ResponseDecodeError{Err: fmt.Errorf("expected '[' for calls, got %v", tok)}
	}

	calls := make([]CallTraceCall, 0, 4)

	for dec.More() {
		call, err := decodeCallFrame(dec, payloadLimit)
		if err != nil {
			return nil, err
		}

		if call != nil {
			calls = append(calls, *call)
		}
	}

	if _, err := dec.Token(); err != nil {
		return nil, asDecodeError(fmt.Errorf("read calls array end: %w", err))
	}

	return calls, nil
}

// decodeJSONObject reads one JSON object, invoking decodeField for every key.
// decodeField must consume exactly the value belonging to the key it is given.
// Reports whether an object was present; a JSON null yields false.
func decodeJSONObject(dec *json.Decoder, what string, decodeField func(key string) error) (bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return false, asDecodeError(fmt.Errorf("read %s start: %w", what, err))
	}

	if tok == nil {
		return false, nil
	}

	if tok != json.Delim('{') {
		return false, &ResponseDecodeError{Err: fmt.Errorf("expected '{' for %s, got %v", what, tok)}
	}

	if err := decodeJSONObjectBody(dec, what, decodeField); err != nil {
		return false, err
	}

	return true, nil
}

// decodeJSONObjectBody reads the members of an object whose opening brace has already
// been read, invoking decodeField for every key.
func decodeJSONObjectBody(dec *json.Decoder, what string, decodeField func(key string) error) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return asDecodeError(fmt.Errorf("read %s key: %w", what, err))
		}

		key, ok := keyTok.(string)
		if !ok {
			return &ResponseDecodeError{
				Err: fmt.Errorf("expected string key in %s, got %T", what, keyTok),
			}
		}

		if err := decodeField(key); err != nil {
			return err
		}
	}

	if _, err := dec.Token(); err != nil {
		return asDecodeError(fmt.Errorf("read %s end: %w", what, err))
	}

	return nil
}

// decodeScalar decodes the value of a single object field.
func decodeScalar(dec *json.Decoder, key string, target any) error {
	if err := dec.Decode(target); err != nil {
		return asDecodeError(fmt.Errorf("decode %s: %w", key, err))
	}

	return nil
}

// skipJSONValue consumes exactly one JSON value without materialising it.
func skipJSONValue(dec *json.Decoder) error {
	depth := 0

	for {
		tok, err := dec.Token()
		if err != nil {
			return asDecodeError(fmt.Errorf("skip value: %w", err))
		}

		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '[', '{':
				depth++
			case ']', '}':
				depth--
			}
		}

		if depth == 0 {
			return nil
		}
	}
}

// decodePrunedPayload decodes a hex payload field, keeping at most limit+1
// bytes of it.
func decodePrunedPayload(dec *json.Decoder, key string, limit int, lenient bool, out *[]byte) error {
	payload := prunedHex{limit: limit, lenient: lenient}

	if err := dec.Decode(&payload); err != nil {
		return asDecodeError(fmt.Errorf("decode %s: %w", key, err))
	}

	*out = payload.data

	return nil
}

// prunedHex decodes a hex string into at most limit+1 bytes. Keeping one byte
// past the limit is what marks the value as truncated for later consumers, see
// blockdb/types.TrimPrunedPayload.
//
// The bytes handed to UnmarshalJSON alias the decoder's own buffer, so the
// oversized part of a payload is discarded without ever being copied: what the
// tracer sent is bounded by the response itself, what dora keeps is bounded by
// the limit, and nothing in between is allocated.
//
// A lenient payload that is not valid hex is kept as raw text, matching
// LenientHexBytes - some clients report revert reasons in plain text. A limit
// of zero or less keeps payloads intact.
type prunedHex struct {
	limit   int
	lenient bool
	data    []byte
}

func (p *prunedHex) UnmarshalJSON(input []byte) error {
	if bytes.Equal(input, []byte("null")) {
		p.data = nil

		return nil
	}

	// Hex payloads carry no escape sequences, so the quoted form can be sliced
	// as-is. Anything else goes through the regular string decoder first.
	if len(input) >= 2 && input[0] == '"' && input[len(input)-1] == '"' &&
		bytes.IndexByte(input, '\\') < 0 {
		return p.store(input[1 : len(input)-1])
	}

	var str string
	if err := json.Unmarshal(input, &str); err != nil {
		return fmt.Errorf("expected hex string: %w", err)
	}

	return p.store([]byte(str))
}

// store decodes the unquoted body of a hex payload, truncating it first.
func (p *prunedHex) store(body []byte) error {
	if len(body) >= 2 && body[0] == '0' && (body[1] == 'x' || body[1] == 'X') {
		body = body[2:]
	}

	if maxChars := 2 * (p.limit + 1); p.limit > 0 && len(body) > maxChars {
		body = body[:maxChars]
	}

	if len(body) == 0 {
		p.data = nil

		return nil
	}

	decoded := make([]byte, len(body)/2)
	if _, err := hex.Decode(decoded, body); err != nil {
		if !p.lenient {
			return fmt.Errorf("decode hex payload: %w", err)
		}

		// body aliases the decoder buffer and must not outlive this call.
		p.data = bytes.Clone(body)

		return nil
	}

	p.data = decoded

	return nil
}
