package rpc

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// PrestateTracerConfig config for debug_traceBlockByHash with prestateTracer.
type PrestateTracerConfig struct {
	Tracer       string                    `json:"tracer"`
	TracerConfig PrestateTracerConfigInner `json:"tracerConfig"`
}

type PrestateTracerConfigInner struct {
	DiffMode bool `json:"diffMode"`
}

// PrestateAccount is a single account snapshot from the prestateTracer output.
// Note: JSON keys for account/storage maps are hex strings.
type PrestateAccount struct {
	Balance *hexutil.Big               `json:"balance,omitempty"`
	Nonce   *LenientUint64             `json:"nonce,omitempty"`
	Code    LenientHexBytes            `json:"code,omitempty"`
	Storage map[string]LenientHexBytes `json:"storage,omitempty"` // slot -> 32-byte value
}

// StateDiff is the diffMode output: two maps of touched accounts (pre and post).
type StateDiff struct {
	Pre  map[string]PrestateAccount `json:"pre,omitempty"`
	Post map[string]PrestateAccount `json:"post,omitempty"`
}

// StateDiffResult is the debug_traceBlockByHash result wrapper for one tx.
type StateDiffResult struct {
	TxHash common.Hash `json:"txHash"`
	Result *StateDiff  `json:"result"`
}

// TraceBlockStateDiffsByHash calls debug_traceBlockByHash with prestateTracer in
// diffMode, returning one diff result per transaction.
// Uses streaming JSON decoding to avoid buffering the entire (potentially
// hundreds of MB) response as an intermediate json.RawMessage.
func (ec *ExecutionClient) TraceBlockStateDiffsByHash(
	ctx context.Context,
	blockHash common.Hash,
) ([]StateDiffResult, error) {
	tracerConfig := PrestateTracerConfig{
		Tracer: "prestateTracer",
		TracerConfig: PrestateTracerConfigInner{
			DiffMode: true,
		},
	}

	var results []StateDiffResult

	err := ec.streamRPCCall(ctx, "debug_traceBlockByHash",
		streamDecodeArray(&results),
		blockHash, tracerConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("debug_traceBlockByHash(prestateTracer) failed: %w", err)
	}

	return results, nil
}
