package blockdb

import (
	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

type BlockDb struct {
	engine types.BlockDbEngine
}

var GlobalBlockDb *BlockDb

func InitWithPebble(config dtypes.PebbleDatabaseConfig) error {
	engine, err := pebble.NewPebbleEngine(config)
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

func (db *BlockDb) GetBlockHeader(root []byte) ([]byte, uint64, error) {
	return db.engine.GetBlockHeader(root)
}

func (db *BlockDb) GetBlockBody(root []byte, parser func(uint64, []byte) (interface{}, error)) (interface{}, error) {
	return db.engine.GetBlockBody(root, parser)
}

func (db *BlockDb) AddBlockBody(root []byte, version uint64, block []byte) error {
	return db.engine.AddBlockBody(root, version, block)
}

func (db *BlockDb) AddBlockHeader(root []byte, version uint64, header []byte) (bool, error) {
	return db.engine.AddBlockHeader(root, version, header)
}
