package types

import "context"

// BlockData contains all data components for a block.
type BlockData struct {
	// Header data
	HeaderVersion uint64
	HeaderData    []byte

	// Body data
	BodyVersion uint64
	BodyData    []byte
	Body        any // Parsed body (optional)

	// Execution payload data (ePBS)
	PayloadVersion uint64
	PayloadData    []byte
	Payload        any // Parsed payload (optional)

	// Block access list data
	BalVersion uint64
	BalData    []byte
}

// ExecDataTxSections holds all compressed section data for a single
// transaction. Returned by GetExecDataTxSections so callers get everything
// in one call (backed by a single range read for S3, single key lookup
// for Pebble).
type ExecDataTxSections struct {
	ReceiptMetaData []byte // snappy-compressed, nil if section not present
	EventsData      []byte // snappy-compressed, nil if section not present
	CallTraceData   []byte // snappy-compressed, nil if section not present
	StateChangeData []byte // snappy-compressed, nil if section not present
}

// BlockDbEngine defines the interface for block database engines.
type BlockDbEngine interface {
	// Close closes the database engine.
	Close() error

	// GetBlock retrieves block data with selective loading based on flags.
	// If parseBlock is nil, raw body data is stored in BlockData.BodyData.
	// If parsePayload is nil, raw payload data is stored in BlockData.PayloadData.
	GetBlock(
		ctx context.Context,
		slot uint64,
		root []byte,
		flags BlockDataFlags,
		parseBlock func(uint64, []byte) (any, error),
		parsePayload func(uint64, []byte) (any, error),
	) (*BlockData, error)

	// AddBlock stores block data. Returns:
	// - added: true if a new block was created
	// - updated: true if an existing block was updated with new components
	AddBlock(
		ctx context.Context,
		slot uint64,
		root []byte,
		dataCb func() (*BlockData, error),
	) (added bool, updated bool, err error)

	// GetStoredComponents returns which components exist for a block.
	GetStoredComponents(ctx context.Context, slot uint64, root []byte) (BlockDataFlags, error)
}

// ExecDataEngine is the interface for per-block execution data storage.
// Execution data (events, traces, state changes) is stored separately from
// beacon block data, keyed by slot+blockRoot for efficient range-based pruning.
type ExecDataEngine interface {
	// AddExecData stores execution data for a block.
	// Returns the stored object size in bytes.
	AddExecData(ctx context.Context, slot uint64, blockRoot []byte, data []byte) (int64, error)

	// GetExecData retrieves full execution data for a block.
	// Returns nil, nil if not found.
	GetExecData(ctx context.Context, slot uint64, blockRoot []byte) ([]byte, error)

	// GetExecDataRange retrieves a byte range of execution data.
	// For S3: uses Range header. For Pebble: reads full value and slices.
	// Returns nil, nil if not found.
	GetExecDataRange(ctx context.Context, slot uint64, blockRoot []byte, offset int64, length int64) ([]byte, error)

	// GetExecDataTxSections retrieves compressed section data for a single
	// transaction without loading the entire exec data object.
	// sections is a bitmask of ExecDataSection* constants selecting which
	// sections to return. Contiguous requested sections are fetched in a
	// single range read (S3) or key lookup (Pebble).
	// Returns nil, nil if the transaction is not found.
	GetExecDataTxSections(ctx context.Context, slot uint64, blockRoot []byte, txHash []byte, sections uint32) (*ExecDataTxSections, error)

	// HasExecData checks if execution data exists for a block.
	HasExecData(ctx context.Context, slot uint64, blockRoot []byte) (bool, error)

	// DeleteExecData deletes execution data for a specific block.
	DeleteExecData(ctx context.Context, slot uint64, blockRoot []byte) error

	// PruneExecDataBefore deletes execution data for all slots before maxSlot.
	// Returns the number of objects deleted.
	PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error)
}
