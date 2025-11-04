package execution

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

// TraceIndexer handles internal transaction indexing
type TraceIndexer struct {
	indexerCtx *IndexerCtx
	logger     logrus.FieldLogger
}

// NewTraceIndexer creates a new trace indexer
func NewTraceIndexer(indexerCtx *IndexerCtx, logger logrus.FieldLogger) *TraceIndexer {
	return &TraceIndexer{
		indexerCtx: indexerCtx,
		logger:     logger,
	}
}

// CallTrace represents a call trace from debug_traceTransaction
type CallTrace struct {
	Type    string      `json:"type"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Value   string      `json:"value"`
	Gas     string      `json:"gas"`
	GasUsed string      `json:"gasUsed"`
	Input   string      `json:"input"`
	Output  string      `json:"output"`
	Error   string      `json:"error,omitempty"`
	Calls   []CallTrace `json:"calls,omitempty"`
}

// TraceResult is the result from debug_traceTransaction
type TraceResult struct {
	Result CallTrace `json:"result"`
}

// ProcessTransaction processes internal transactions for a transaction using debug_traceTransaction
func (ti *TraceIndexer) ProcessTransaction(txHash common.Hash, blockNumber uint64, forkId uint64) error {
	// Get a trace-enabled execution client
	client := ti.getTraceClient()
	if client == nil {
		ti.logger.Debug("No trace-enabled execution client available")
		return nil
	}

	ctx := context.Background()

	// Call debug_traceTransaction with callTracer
	var result json.RawMessage
	err := client.GetRPC().CallContext(ctx, &result, "debug_traceTransaction", txHash.Hex(), map[string]interface{}{
		"tracer": "callTracer",
	})
	if err != nil {
		// Some clients may not support debug_traceTransaction, log and skip
		ti.logger.WithError(err).Debug("Failed to trace transaction (client may not support tracing)")
		return nil
	}

	// Parse the trace result
	var trace CallTrace
	if err := json.Unmarshal(result, &trace); err != nil {
		return err
	}

	// Recursively extract internal transactions
	internalTxs := ti.extractInternalTransactions(&trace, txHash, blockNumber, 0, forkId)

	if len(internalTxs) == 0 {
		return nil
	}

	// Save to database
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertElInternalTxs(internalTxs, tx)
	})
}

// getTraceClient returns an execution client that supports tracing
func (ti *TraceIndexer) getTraceClient() *execution.Client {
	// Check if we have any endpoints with trace flag enabled
	for _, endpoint := range utils.Config.ExecutionApi.Endpoints {
		if endpoint.Trace {
			// Get client for this specific endpoint
			client := ti.indexerCtx.executionPool.GetReadyEndpoint(func(client *execution.Client) bool {
				return client.GetName() == endpoint.Name
			})
			if client != nil {
				return client
			}
		}
	}

	// Fall back to any ready client (may not support tracing)
	return ti.indexerCtx.executionPool.GetReadyEndpoint(nil)
}

// extractInternalTransactions recursively extracts internal transactions from trace
func (ti *TraceIndexer) extractInternalTransactions(trace *CallTrace, txHash common.Hash, blockNumber uint64, depth uint, forkId uint64) []*dbtypes.ElInternalTx {
	var result []*dbtypes.ElInternalTx

	// Parse addresses
	fromAddr := common.HexToAddress(trace.From)
	var toAddr *common.Address
	if trace.To != "" {
		addr := common.HexToAddress(trace.To)
		toAddr = &addr
	}

	// Parse value
	var value []byte
	if trace.Value != "" {
		val, ok := new(big.Int).SetString(trace.Value, 0) // 0 means auto-detect base (hex/decimal)
		if !ok {
			// Try hex decoding
			if decoded, err := hexutil.DecodeBig(trace.Value); err == nil {
				value = decoded.Bytes()
			} else {
				value = []byte{0}
			}
		} else {
			value = val.Bytes()
		}
	} else {
		value = []byte{0}
	}

	// Parse gas
	var gasLimit uint64
	if trace.Gas != "" {
		if decoded, err := hexutil.DecodeUint64(trace.Gas); err == nil {
			gasLimit = decoded
		}
	}

	var gasUsed uint64
	if trace.GasUsed != "" {
		if decoded, err := hexutil.DecodeUint64(trace.GasUsed); err == nil {
			gasUsed = decoded
		}
	}

	// Parse input data
	var input []byte
	if trace.Input != "" {
		if decoded, err := hexutil.Decode(trace.Input); err == nil {
			input = decoded
		}
	}

	// Create internal transaction entry
	internalTx := &dbtypes.ElInternalTx{
		TransactionHash: txHash.Bytes(),
		BlockNumber:     blockNumber,
		TraceType:       trace.Type,
		FromAddress:     fromAddr.Bytes(),
		ToAddress:       nil,
		Value:           value,
		GasLimit:        gasLimit,
		GasUsed:         gasUsed,
		InputData:       input,
		TraceIndex:      depth,
		ForkId:          forkId,
	}

	if toAddr != nil {
		internalTx.ToAddress = toAddr.Bytes()
	}

	// Set error flag if present
	if trace.Error != "" {
		errMsg := trace.Error
		internalTx.Error = &errMsg
	}

	// Only add if it's an actual internal call (depth > 0) or a valuable root call
	if depth > 0 || trace.Type != "CALL" {
		result = append(result, internalTx)
	}

	// Recursively process child calls
	for i, call := range trace.Calls {
		childDepth := depth + uint(i) + 1
		childTxs := ti.extractInternalTransactions(&call, txHash, blockNumber, childDepth, forkId)
		result = append(result, childTxs...)
	}

	return result
}
