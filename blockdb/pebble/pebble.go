package pebble

import (
	"encoding/binary"

	"github.com/cockroachdb/pebble"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

const (
	KeyNamespaceBlock uint16 = 1
)

const (
	BlockTypeHeader uint16 = 1
	BlockTypeBody   uint16 = 2
)

type PebbleEngine struct {
	db *pebble.DB
}

func NewPebbleEngine(config dtypes.PebbleDatabaseConfig) (types.BlockDbEngine, error) {
	cache := pebble.NewCache(int64(config.CacheSize * 1024 * 1024))
	defer cache.Unref()

	db, err := pebble.Open(config.Path, &pebble.Options{
		Cache: cache,
	})
	if err != nil {
		return nil, err
	}

	return &PebbleEngine{
		db: db,
	}, nil
}

func (e *PebbleEngine) Close() error {
	err := e.db.Close()
	if err != nil {
		return err
	}

	return nil
}

func (e *PebbleEngine) GetBlockHeader(root []byte) ([]byte, uint64, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeHeader)

	res, closer, err := e.db.Get(key)
	if err != nil {
		return nil, 0, err
	}
	defer closer.Close()

	if len(res) == 0 {
		return nil, 0, nil
	}

	version := binary.BigEndian.Uint64(res[:8])
	header := make([]byte, len(res)-8)
	copy(header, res[8:])

	return header, version, nil
}

func (e *PebbleEngine) GetBlockBody(root []byte, parser func(uint64, []byte) (interface{}, error)) (interface{}, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeBody)

	res, closer, err := e.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	if len(res) == 0 {
		return nil, nil
	}

	version := binary.BigEndian.Uint64(res[:8])
	block := res[8:]

	return parser(version, block)
}

func (e *PebbleEngine) AddBlockHeader(root []byte, version uint64, header []byte) (bool, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeHeader)

	res, closer, err := e.db.Get(key)
	if err != nil {
		return false, err
	}
	defer closer.Close()

	if len(res) > 0 {
		return false, nil
	}

	data := make([]byte, 8+len(header))
	binary.BigEndian.PutUint64(data[:8], version)

	return true, e.db.Set(key, data, nil)
}

func (e *PebbleEngine) AddBlockBody(root []byte, version uint64, block []byte) error {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeBody)

	data := make([]byte, 8+len(block))
	binary.BigEndian.PutUint64(data[:8], version)
	copy(data[8:], block)

	return e.db.Set(key, data, nil)
}
