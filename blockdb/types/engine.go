package types

import "context"

// BlockData holds beacon block header and body data.
type BlockData struct {
	HeaderVersion uint64
	HeaderData    []byte
	BodyVersion   uint64
	BodyData      []byte
	Body          interface{}
}

// BlockDbEngine is the interface for beacon block storage.
type BlockDbEngine interface {
	Close() error
	GetBlock(ctx context.Context, slot uint64, root []byte, parseBlock func(uint64, []byte) (interface{}, error)) (*BlockData, error)
	AddBlock(ctx context.Context, slot uint64, root []byte, dataCb func() (*BlockData, error)) (bool, error)
}

// ExecDataEngine is the interface for per-block execution data storage.
// Execution data (events, traces, state changes) is stored separately from
// beacon block data, keyed by slot+blockHash for efficient range-based pruning.
type ExecDataEngine interface {
	// AddExecData stores execution data for a block.
	// Returns the stored object size in bytes.
	AddExecData(ctx context.Context, slot uint64, blockHash []byte, data []byte) (int64, error)

	// GetExecData retrieves full execution data for a block.
	// Returns nil, nil if not found.
	GetExecData(ctx context.Context, slot uint64, blockHash []byte) ([]byte, error)

	// GetExecDataRange retrieves a byte range of execution data.
	// For S3: uses Range header. For Pebble: reads full value and slices.
	// Returns nil, nil if not found.
	GetExecDataRange(ctx context.Context, slot uint64, blockHash []byte, offset int64, length int64) ([]byte, error)

	// HasExecData checks if execution data exists for a block.
	HasExecData(ctx context.Context, slot uint64, blockHash []byte) (bool, error)

	// DeleteExecData deletes execution data for a specific block.
	DeleteExecData(ctx context.Context, slot uint64, blockHash []byte) error

	// PruneExecDataBefore deletes execution data for all slots before maxSlot.
	// Returns the number of objects deleted.
	PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error)
}
