package blockdb

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/blockdb/tiered"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

// BlockDb is the main wrapper for block database operations.
type BlockDb struct {
	engine types.BlockDbEngine
}

// GlobalBlockDb is the global block database instance.
var GlobalBlockDb *BlockDb

// InitWithPebble initializes the block database with Pebble (local) storage.
func InitWithPebble(config dtypes.PebbleBlockDBConfig) error {
	engine, err := pebble.NewPebbleEngine(config)
	if err != nil {
		return err
	}

	GlobalBlockDb = &BlockDb{
		engine: engine,
	}

	return nil
}

// InitWithS3 initializes the block database with S3 (remote) storage.
func InitWithS3(config dtypes.S3BlockDBConfig) error {
	engine, err := s3.NewS3Engine(config)
	if err != nil {
		return err
	}

	GlobalBlockDb = &BlockDb{
		engine: engine,
	}

	return nil
}

// InitWithTiered initializes the block database with tiered storage (Pebble cache + S3 backend).
func InitWithTiered(config dtypes.TieredBlockDBConfig, logger logrus.FieldLogger) error {
	engine, err := tiered.NewTieredEngine(config, logger)
	if err != nil {
		return err
	}

	GlobalBlockDb = &BlockDb{
		engine: engine,
	}

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
