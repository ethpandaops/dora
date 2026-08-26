package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeTraceArray runs the streaming call trace decoder over a raw result array.
func decodeTraceArray(t *testing.T, body string, payloadLimit int) ([]CallTraceResult, error) {
	t.Helper()

	var results []CallTraceResult

	err := decodeCallTraceResults(&results, payloadLimit)(json.NewDecoder(strings.NewReader(body)))

	return results, err
}

func TestDecodeCallTraceResults(t *testing.T) {
	body := `[
		{
			"txHash": "0x2222222222222222222222222222222222222222222222222222222222222222",
			"result": {
				"type": "CALL",
				"from": "0xafe6578eb95f3030b7ffabbf6011a1dc2a4d7967",
				"to": "0x722d316672c8be206c3d228d087d1dc948a61345",
				"value": "0x1bc16d674ec80000",
				"gas": "0x1000000",
				"gasUsed": "0xfb8bd7",
				"input": "0x00000055",
				"output": "0xdeadbeef",
				"calls": [
					{
						"type": "STATICCALL",
						"from": "0x722d316672c8be206c3d228d087d1dc948a61345",
						"to": "0x0000000000000000000000000000000000000004",
						"gas": "0x100",
						"gasUsed": "0x30",
						"input": "0xaabb",
						"output": "0xaabb",
						"unknownField": {"nested": [1, 2, {"deep": true}]}
					},
					{
						"type": "DELEGATECALL",
						"from": "0x722d316672c8be206c3d228d087d1dc948a61345",
						"to": "0x1111111111111111111111111111111111111111",
						"gas": "0x50",
						"gasUsed": "0x10",
						"input": "0x",
						"error": "execution reverted",
						"calls": null
					}
				]
			}
		},
		{
			"txHash": "0x3333333333333333333333333333333333333333333333333333333333333333",
			"result": null
		}
	]`

	results, err := decodeTraceArray(t, body, 1024)
	require.NoError(t, err)
	require.Len(t, results, 2)

	root := results[0].Result
	require.NotNil(t, root)
	assert.Equal(t, "CALL", root.Type)
	assert.Equal(t, "0x722d316672c8be206c3d228d087d1dc948a61345", strings.ToLower(root.To.Hex()))
	assert.Equal(t, uint64(0x1000000), uint64(root.Gas))
	assert.Equal(t, uint64(0xfb8bd7), uint64(root.GasUsed))
	assert.Equal(t, "1bc16d674ec80000", root.Value.ToInt().Text(16))
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x55}, []byte(root.Input))
	assert.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, []byte(root.Output))
	require.Len(t, root.Calls, 2)

	assert.Equal(t, "STATICCALL", root.Calls[0].Type)
	assert.Equal(t, []byte{0xaa, 0xbb}, []byte(root.Calls[0].Input))

	assert.Equal(t, "DELEGATECALL", root.Calls[1].Type)
	assert.Equal(t, "execution reverted", root.Calls[1].Error)
	assert.Empty(t, root.Calls[1].Input)
	assert.Nil(t, root.Calls[1].Calls)

	assert.Nil(t, results[1].Result)
}

func TestDecodeCallTraceResultsPrunesPayloads(t *testing.T) {
	const limit = 64

	// A frame that carries far more payload than the limit, the shape the
	// memory-inflating trace attack produces on every one of its call frames.
	oversized := strings.Repeat("ab", 8192)
	body := fmt.Sprintf(`[{"txHash":"0x%064x","result":{
		"type":"STATICCALL",
		"from":"0x%040x","to":"0x%040x",
		"gas":"0x1","gasUsed":"0x1",
		"input":"0x%s","output":"0x%s"
	}}]`, 1, 2, 4, oversized, oversized)

	results, err := decodeTraceArray(t, body, limit)
	require.NoError(t, err)
	require.Len(t, results, 1)

	root := results[0].Result
	require.NotNil(t, root)

	// One byte past the limit is what marks the payload as truncated.
	assert.Len(t, []byte(root.Input), limit+1)
	assert.Len(t, []byte(root.Output), limit+1)
	assert.Equal(t, byte(0xab), root.Input[limit])
}

func TestDecodeCallTraceResultsKeepsPayloadsWithinLimit(t *testing.T) {
	body := `[{"txHash":"0x2222222222222222222222222222222222222222222222222222222222222222",
		"result":{"type":"CALL","from":"0x0000000000000000000000000000000000000001",
		"to":"0x0000000000000000000000000000000000000002","gas":"0x1","gasUsed":"0x1",
		"input":"0x0102030405"}}]`

	results, err := decodeTraceArray(t, body, 5)
	require.NoError(t, err)
	require.Len(t, results, 1)

	// Exactly at the limit, so nothing is truncated and no marker byte is added.
	assert.Equal(t, []byte{1, 2, 3, 4, 5}, []byte(results[0].Result.Input))
}

func TestDecodeCallTraceResultsNullAndEmpty(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "null result", body: `null`},
		{name: "empty array", body: `[]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, err := decodeTraceArray(t, test.body, 1024)
			require.NoError(t, err)
			assert.Empty(t, results)
		})
	}
}

func TestDecodeCallTraceResultsLenientRevertReason(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		expected []byte
	}{
		{name: "hex", reason: "0x1234", expected: []byte{0x12, 0x34}},
		{name: "unprefixed hex", reason: "1234", expected: []byte{0x12, 0x34}},
		{name: "plain text", reason: "insufficient balance", expected: []byte("insufficient balance")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`[{"txHash":"0x%064x","result":{"type":"CALL",
				"from":"0x%040x","to":"0x%040x","gas":"0x1","gasUsed":"0x1",
				"revertReason":%q}}]`, 1, 2, 3, test.reason)

			results, err := decodeTraceArray(t, body, 1024)
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, test.expected, []byte(results[0].Result.RevertReason))
		})
	}
}

func TestDecodeCallTraceResultsStructuralErrorIsNotRetryable(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "not an array", body: `{"type":"CALL"}`},
		{
			name: "frame is not an object",
			body: `[{"txHash":"0x2222222222222222222222222222222222222222222222222222222222222222",
				"result":"CALL"}]`,
		},
		{
			name: "wrong field type",
			body: `[{"txHash":"0x2222222222222222222222222222222222222222222222222222222222222222",
				"result":{"type":[1,2,3]}}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeTraceArray(t, test.body, 1024)
			require.Error(t, err)

			var decodeErr *ResponseDecodeError
			assert.True(t, errors.As(err, &decodeErr),
				"expected a ResponseDecodeError, got %T: %v", err, err)
		})
	}
}

func TestDecodeCallTraceResultsTruncatedResponseIsRetryable(t *testing.T) {
	// A body that stops mid-array is an I/O level problem, so it must stay
	// retryable against another client.
	_, err := decodeTraceArray(t, `[{"txHash":"0x2222","result":{"type":"CA`, 1024)
	require.Error(t, err)

	var decodeErr *ResponseDecodeError
	assert.False(t, errors.As(err, &decodeErr), "unexpected ResponseDecodeError: %v", err)
}

func TestSkipJSONValue(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(`{"a":[1,{"b":[[]]},null],"c":42}`))

	_, err := dec.Token() // '{'
	require.NoError(t, err)

	key, err := dec.Token() // "a"
	require.NoError(t, err)
	require.Equal(t, "a", key)

	require.NoError(t, skipJSONValue(dec))

	// The skip must land exactly on the next key.
	key, err = dec.Token()
	require.NoError(t, err)
	assert.Equal(t, "c", key)
}
