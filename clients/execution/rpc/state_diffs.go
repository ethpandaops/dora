package rpc

import (
	"context"
	"encoding/json"
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

	var raw json.RawMessage
	if err := ec.rpcClient.CallContext(ctx, &raw, "debug_traceBlockByHash", blockHash, tracerConfig); err != nil {
		return nil, fmt.Errorf("debug_traceBlockByHash(prestateTracer) failed: %w", err)
	}

	var results []StateDiffResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal prestate trace results: %w", err)
	}

	return results, nil
}
