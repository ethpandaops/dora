package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/holiman/uint256"
)

// LenientHexBytes is like hexutil.Bytes, but accepts hex strings with or without
// a 0x prefix (some clients return revertReason without 0x) and also accepts
// plain-text strings (some clients return revertReason as human-readable text).
type LenientHexBytes []byte

func (b *LenientHexBytes) UnmarshalJSON(input []byte) error {
	// null
	if len(input) == 4 && string(input) == "null" {
		*b = nil
		return nil
	}

	// Typically encoded as JSON string
	var s string
	if err := json.Unmarshal(input, &s); err != nil {
		// Fall back to strict hexutil.Bytes
		var hb hexutil.Bytes
		if err2 := json.Unmarshal(input, &hb); err2 != nil {
			return err
		}
		*b = LenientHexBytes(hb)
		return nil
	}

	if s == "" {
		*b = nil
		return nil
	}

	hexStr := s
	if !strings.HasPrefix(hexStr, "0x") && !strings.HasPrefix(hexStr, "0X") {
		hexStr = "0x" + hexStr
	}

	decoded, err := hexutil.Decode(hexStr)
	if err != nil {
		// Not valid hex; treat as raw UTF-8 text (e.g. plain-text revert reasons).
		*b = []byte(s)
		return nil
	}
	*b = LenientHexBytes(decoded)
	return nil
}

// CallTracerConfig is the configuration for the callTracer.
type CallTracerConfig struct {
	Tracer string `json:"tracer"`
}

// CallTraceResult is the JSON result for one transaction from
// debug_traceBlockByHash with the callTracer.
type CallTraceResult struct {
	TxHash common.Hash    `json:"txHash"`
	Result *CallTraceCall `json:"result"`
}

// CallTraceCall is a single call frame in the callTracer output.
type CallTraceCall struct {
	Type    string          `json:"type"`
	From    common.Address  `json:"from"`
	To      common.Address  `json:"to"`
	Value   *hexutil.Big    `json:"value,omitempty"`
	Gas     hexutil.Uint64  `json:"gas"`
	GasUsed hexutil.Uint64  `json:"gasUsed"`
	Input   hexutil.Bytes   `json:"input"`
	Output  hexutil.Bytes   `json:"output"`
	Error   string          `json:"error,omitempty"`
	Calls   []CallTraceCall `json:"calls,omitempty"`

	// Revert reason (some clients include this separately)
	RevertReason LenientHexBytes `json:"revertReason,omitempty"`
}

// CallTypeFromString converts a callTracer type string to a numeric call type.
// Returns: 0=CALL, 1=STATICCALL, 2=DELEGATECALL, 3=CREATE, 4=CREATE2, 5=SELFDESTRUCT.
func CallTypeFromString(s string) uint8 {
	switch s {
	case "CALL":
		return 0
	case "STATICCALL":
		return 1
	case "DELEGATECALL":
		return 2
	case "CREATE":
		return 3
	case "CREATE2":
		return 4
	case "SELFDESTRUCT":
		return 5
	default:
		return 0 // Default to CALL for unknown types
	}
}

// CallTraceCallValue returns the big.Int value from a CallTraceCall, or zero if nil.
func CallTraceCallValue(c *CallTraceCall) uint256.Int {
	if c.Value == nil {
		return uint256.Int{}
	}
	return *uint256.MustFromBig(c.Value.ToInt())
}

// TraceBlockByHash calls debug_traceBlockByHash with the callTracer configuration.
// Returns one CallTraceResult per transaction in the block.
// The response can be very large for complex blocks; this implementation uses
// json.RawMessage to avoid buffering the entire decoded structure at once.
func (ec *ExecutionClient) TraceBlockByHash(
	ctx context.Context,
	blockHash common.Hash,
) ([]CallTraceResult, error) {
	tracerConfig := CallTracerConfig{
		Tracer: "callTracer",
	}

	var raw json.RawMessage
	err := ec.rpcClient.CallContext(ctx, &raw, "debug_traceBlockByHash", blockHash, tracerConfig)
	if err != nil {
		return nil, fmt.Errorf("debug_traceBlockByHash failed: %w", err)
	}

	var results []CallTraceResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal trace results: %w", err)
	}

	return results, nil
}
