package blockdb

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/blockdb/tiered"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

// BlockDb is the main wrapper for block database operations.
type BlockDb struct {
	engine     types.BlockDbEngine
	execEngine types.ExecDataEngine // nil if engine doesn't support exec data
}

// GlobalBlockDb is the global block database instance.
var GlobalBlockDb *BlockDb

// InitWithPebble initializes the block database with Pebble (local) storage.
func InitWithPebble(config dtypes.PebbleBlockDBConfig) error {
	engine, err := pebble.NewPebbleEngine(config)
	if err != nil {
		return err
	}

	db := &BlockDb{
		engine: engine,
	}

	// Pebble engine always supports exec data
	if execEngine, ok := engine.(types.ExecDataEngine); ok {
		db.execEngine = execEngine
	}

	GlobalBlockDb = db

	return nil
}

// InitWithS3 initializes the block database with S3 (remote) storage.
func InitWithS3(config dtypes.S3BlockDBConfig) error {
	engine, err := s3.NewS3Engine(config)
	if err != nil {
		return err
	}

	db := &BlockDb{
		engine: engine,
	}

	// S3 engine always supports exec data
	if execEngine, ok := engine.(types.ExecDataEngine); ok {
		db.execEngine = execEngine
	}

	GlobalBlockDb = db

	return nil
}

// InitWithTiered initializes the block database with tiered storage (Pebble cache + S3 backend).
func InitWithTiered(config dtypes.TieredBlockDBConfig, logger logrus.FieldLogger) error {
	engine, err := tiered.NewTieredEngine(config, logger)
	if err != nil {
		return err
	}

	db := &BlockDb{
		engine: engine,
	}

	// Check if tiered engine supports exec data
	if execEngine, ok := engine.(types.ExecDataEngine); ok {
		db.execEngine = execEngine
	}

	GlobalBlockDb = db

	return nil
}

// Close closes the block database.
func (db *BlockDb) Close() error {
	return db.engine.Close()
}

// GetBlock retrieves block data with selective loading based on flags.
func (db *BlockDb) GetBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	return db.engine.GetBlock(ctx, slot, root, flags, parseBlock, parsePayload)
}

// GetStoredComponents returns which components exist for a block.
func (db *BlockDb) GetStoredComponents(ctx context.Context, slot uint64, root []byte) (types.BlockDataFlags, error) {
	return db.engine.GetStoredComponents(ctx, slot, root)
}

// AddBlock stores block data. Returns (added, updated, error).
func (db *BlockDb) AddBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	headerVer uint64,
	headerData []byte,
	bodyVer uint64,
	bodyData []byte,
	payloadVer uint64,
	payloadData []byte,
	balVer uint64,
	balData []byte,
) (bool, bool, error) {
	return db.engine.AddBlock(ctx, slot, root, func() (*types.BlockData, error) {
		return &types.BlockData{
			HeaderVersion:  headerVer,
			HeaderData:     headerData,
			BodyVersion:    bodyVer,
			BodyData:       bodyData,
			PayloadVersion: payloadVer,
			PayloadData:    payloadData,
			BalVersion:     balVer,
			BalData:        balData,
		}, nil
	})
}

// AddBlockWithCallback stores block data using a callback for deferred data loading.
// Returns (added, updated, error).
func (db *BlockDb) AddBlockWithCallback(
	ctx context.Context,
	slot uint64,
	root []byte,
	dataCb func() (*types.BlockData, error),
) (bool, bool, error) {
	return db.engine.AddBlock(ctx, slot, root, dataCb)
}

// SupportsExecData returns true if the underlying engine supports execution data storage.
func (db *BlockDb) SupportsExecData() bool {
	return db.execEngine != nil
}

// AddExecData stores execution data for a block. Returns stored size.
func (db *BlockDb) AddExecData(ctx context.Context, slot uint64, blockRoot []byte, data []byte) (int64, error) {
	if db.execEngine == nil {
		return 0, fmt.Errorf("exec data not supported by engine")
	}
	return db.execEngine.AddExecData(ctx, slot, blockRoot, data)
}

// GetExecData retrieves full execution data for a block.
func (db *BlockDb) GetExecData(ctx context.Context, slot uint64, blockRoot []byte) ([]byte, error) {
	if db.execEngine == nil {
		return nil, fmt.Errorf("exec data not supported by engine")
	}
	return db.execEngine.GetExecData(ctx, slot, blockRoot)
}

// GetExecDataRange retrieves a byte range of execution data.
func (db *BlockDb) GetExecDataRange(ctx context.Context, slot uint64, blockRoot []byte, offset int64, length int64) ([]byte, error) {
	if db.execEngine == nil {
		return nil, fmt.Errorf("exec data not supported by engine")
	}
	return db.execEngine.GetExecDataRange(ctx, slot, blockRoot, offset, length)
}

// GetExecDataTxSections retrieves compressed section data for a single
// transaction. sections is a bitmask selecting which sections to return.
func (db *BlockDb) GetExecDataTxSections(ctx context.Context, slot uint64, blockRoot []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	if db.execEngine == nil {
		return nil, fmt.Errorf("exec data not supported by engine")
	}
	return db.execEngine.GetExecDataTxSections(ctx, slot, blockRoot, txHash, sections)
}

// HasExecData checks if execution data exists for a block.
func (db *BlockDb) HasExecData(ctx context.Context, slot uint64, blockRoot []byte) (bool, error) {
	if db.execEngine == nil {
		return false, nil
	}
	return db.execEngine.HasExecData(ctx, slot, blockRoot)
}

// DeleteExecData deletes execution data for a specific block.
func (db *BlockDb) DeleteExecData(ctx context.Context, slot uint64, blockRoot []byte) error {
	if db.execEngine == nil {
		return fmt.Errorf("exec data not supported by engine")
	}
	return db.execEngine.DeleteExecData(ctx, slot, blockRoot)
}

// PruneExecDataBefore deletes execution data for all slots before maxSlot.
func (db *BlockDb) PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	if db.execEngine == nil {
		return 0, nil
	}
	return db.execEngine.PruneExecDataBefore(ctx, maxSlot)
}
