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
