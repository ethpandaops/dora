package blockdb

import (
	"context"

	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

type BlockDb struct {
	engine types.BlockDbEngine
}

var GlobalBlockDb *BlockDb

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

func (db *BlockDb) Close() error {
	return db.engine.Close()
}

func (db *BlockDb) GetBlock(ctx context.Context, slot uint64, root []byte, parseBlock func(uint64, []byte) (interface{}, error), parsePayload func(uint64, []byte) (interface{}, error)) (*types.BlockData, error) {
	return db.engine.GetBlock(ctx, slot, root, parseBlock, parsePayload)
}

func (db *BlockDb) AddBlock(ctx context.Context, slot uint64, root []byte, header_ver uint64, header_data []byte, body_ver uint64, body_data []byte, payload_ver uint64, payload_data []byte) (bool, error) {
	return db.engine.AddBlock(ctx, slot, root, func() (*types.BlockData, error) {
		return &types.BlockData{
			HeaderVersion:  header_ver,
			HeaderData:     header_data,
			BodyVersion:    body_ver,
			BodyData:       body_data,
			PayloadVersion: payload_ver,
			PayloadData:    payload_data,
		}, nil
	})
}

func (db *BlockDb) AddBlockWithCallback(ctx context.Context, slot uint64, root []byte, dataCb func() (*types.BlockData, error)) (bool, error) {
	return db.engine.AddBlock(ctx, slot, root, dataCb)
}
