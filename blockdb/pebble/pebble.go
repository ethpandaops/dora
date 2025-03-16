package pebble

import (
	"context"
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

func NewPebbleEngine(config dtypes.PebbleBlockDBConfig) (types.BlockDbEngine, error) {
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

func (e *PebbleEngine) getBlockHeader(root []byte) ([]byte, uint64, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeHeader)

	res, closer, err := e.db.Get(key)
	if err != nil && err != pebble.ErrNotFound {
		return nil, 0, err
	}
	defer closer.Close()

	if err == pebble.ErrNotFound || len(res) == 0 {
		return nil, 0, nil
	}

	version := binary.BigEndian.Uint64(res[:8])
	header := make([]byte, len(res)-8)
	copy(header, res[8:])

	return header, version, nil
}

func (e *PebbleEngine) getBlockBody(root []byte, parser func(uint64, []byte) (interface{}, error)) (interface{}, uint64, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeBody)

	res, closer, err := e.db.Get(key)
	if err != nil && err != pebble.ErrNotFound {
		return nil, 0, err
	}
	defer closer.Close()

	if err == pebble.ErrNotFound || len(res) == 0 {
		return nil, 0, nil
	}

	version := binary.BigEndian.Uint64(res[:8])
	block := res[8:]

	body, err := parser(version, block)
	if err != nil {
		return nil, 0, err
	}

	return body, version, nil
}

func (e *PebbleEngine) GetBlock(_ context.Context, _ uint64, root []byte, parseBlock func(uint64, []byte) (interface{}, error)) (*types.BlockData, error) {
	header, header_ver, err := e.getBlockHeader(root)
	if err != nil {
		return nil, err
	}

	blockData := &types.BlockData{
		HeaderVersion: header_ver,
		HeaderData:    header,
	}

	if parseBlock == nil {
		parseBlock = func(version uint64, block []byte) (interface{}, error) {
			blockData.BodyData = make([]byte, len(block))
			copy(blockData.BodyData, block)
			return nil, nil
		}
	}

	body, body_ver, err := e.getBlockBody(root, parseBlock)
	if err != nil {
		return nil, err
	}

	blockData.Body = body
	blockData.BodyVersion = body_ver

	return blockData, nil
}

func (e *PebbleEngine) checkBlock(key []byte) bool {
	res, closer, err := e.db.Get(key)
	if err == nil && len(res) > 0 {
		closer.Close()
		return true
	}

	return false
}

func (e *PebbleEngine) addBlockHeader(key []byte, version uint64, header []byte) error {
	data := make([]byte, 8+len(header))
	binary.BigEndian.PutUint64(data[:8], version)

	return e.db.Set(key, data, nil)
}

func (e *PebbleEngine) addBlockBody(root []byte, version uint64, block []byte) error {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeBody)

	data := make([]byte, 8+len(block))
	binary.BigEndian.PutUint64(data[:8], version)
	copy(data[8:], block)

	return e.db.Set(key, data, nil)
}

func (e *PebbleEngine) AddBlock(_ context.Context, _ uint64, root []byte, dataCb func() (*types.BlockData, error)) (bool, error) {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], BlockTypeHeader)

	if e.checkBlock(key) {
		return false, nil
	}

	blockData, err := dataCb()
	if err != nil {
		return false, err
	}

	err = e.addBlockHeader(key, blockData.HeaderVersion, blockData.HeaderData)
	if err != nil {
		return false, err
	}

	err = e.addBlockBody(root, blockData.BodyVersion, blockData.BodyData)
	if err != nil {
		return false, err
	}

	return true, nil
}
