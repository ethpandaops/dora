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

// firstRoot returns a transaction's single top-level call frame, or nil when it has none.
func firstRoot(result CallTraceResult) *CallTraceCall {
	if len(result.Roots) == 0 {
		return nil
	}

	return result.Roots[0]
}

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

	root := firstRoot(results[0])
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

	assert.Nil(t, firstRoot(results[1]))
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

	root := firstRoot(results[0])
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
	assert.Equal(t, []byte{1, 2, 3, 4, 5}, []byte(firstRoot(results[0]).Input))
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
			assert.Equal(t, test.expected, []byte(firstRoot(results[0]).RevertReason))
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

// A client that decomposes an EIP-8141 frame transaction has one top-level call per
// frame, and reports them as a list rather than the single object an ordinary
// transaction produces. No client does this yet, so the shape is accepted in advance.
func TestDecodeCallTraceResultsAcceptsMultipleRoots(t *testing.T) {
	const body = `[
		{
			"txHash": "0x1111111111111111111111111111111111111111111111111111111111111111",
			"result": [
				{
					"type": "CALL",
					"from": "0x6df35438a4dfcdbd25c7a364ab77e3cfdce87fc5",
					"to": "0x0000000000000000000000000000000000008141",
					"gas": "0x1388",
					"gasUsed": "0x33",
					"input": "0x"
				},
				{
					"type": "CALL",
					"from": "0x6df35438a4dfcdbd25c7a364ab77e3cfdce87fc5",
					"to": "0x30592ef78d262bc79f0fe46355e07a51d685e382",
					"gas": "0x7530",
					"gasUsed": "0x5208",
					"input": "0xdeadbeef",
					"calls": [
						{
							"type": "STATICCALL",
							"from": "0x30592ef78d262bc79f0fe46355e07a51d685e382",
							"to": "0x0000000000000000000000000000000000000004",
							"gas": "0x100",
							"gasUsed": "0x12",
							"input": "0x"
						}
					]
				}
			]
		}
	]`

	results, err := decodeTraceArray(t, body, 1024)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Roots, 2)

	assert.Equal(t, "0x0000000000000000000000000000000000008141", strings.ToLower(results[0].Roots[0].To.Hex()))
	assert.Equal(t, uint64(0x33), uint64(results[0].Roots[0].GasUsed))

	assert.Equal(t, "0x30592ef78d262bc79f0fe46355e07a51d685e382", strings.ToLower(results[0].Roots[1].To.Hex()))
	require.Len(t, results[0].Roots[1].Calls, 1)
	assert.Equal(t, "STATICCALL", results[0].Roots[1].Calls[0].Type)
}

// Verbatim callTracer output from ethrex (eip8141-v2-lenient) for a four-frame
// transaction: one self-addressed childless placeholder that says nothing about the
// frames, with the EIP-8037 gas dimensions reported and gasUsed left at zero.
func TestDecodeCallTraceResultsReadsEip8037GasDimensions(t *testing.T) {
	const body = `[
		{
			"result": {
				"from": "0x6bcb3483cd582d6011e80805e0c6a90d42b98710",
				"gas": "0x1b888",
				"gasRefund": "0x1664c",
				"gasUsed": "0x0",
				"input": "0x",
				"regularGasUsed": "0x523c",
				"stateGasUsed": "0x0",
				"to": "0x6bcb3483cd582d6011e80805e0c6a90d42b98710",
				"type": "CALL",
				"value": "0x0"
			},
			"txHash": "0x01a68783c4c3fa7af37526d9c34b25e66dea81c44840c28c905faf532c99eff1"
		}
	]`

	results, err := decodeTraceArray(t, body, 1024)
	require.NoError(t, err)
	require.Len(t, results, 1)

	root := firstRoot(results[0])
	require.NotNil(t, root)
	assert.Empty(t, root.Calls, "the placeholder carries no frames")
	assert.Equal(t, root.From, root.To, "the placeholder addresses the sender itself")

	assert.Equal(t, uint64(0x523c), uint64(root.RegularGasUsed))
	assert.Equal(t, uint64(0x1664c), uint64(root.GasRefund))

	// gasUsed is zero here, so the cost has to come from the two dimensions.
	assert.Zero(t, uint64(root.GasUsed))
	assert.Equal(t, uint64(0x523c), root.TotalGasUsed())
}

// Where a client fills gasUsed in, it is authoritative and the dimensions are not summed
// on top of it.
func TestTotalGasUsedPrefersReportedGasUsed(t *testing.T) {
	call := &CallTraceCall{GasUsed: 0x64e86, RegularGasUsed: 0xb426, StateGasUsed: 0x59a60}
	assert.Equal(t, uint64(0x64e86), call.TotalGasUsed())
}
