package pebble

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

const (
	KeyNamespaceBlock uint16 = 1
)

const (
	BlockTypeHeader  uint16 = 1
	BlockTypeBody    uint16 = 2
	BlockTypePayload uint16 = 3
	BlockTypeBal     uint16 = 4
)

// Value format: [version (8 bytes)] [timestamp (8 bytes)] [data]
const valueHeaderSize = 16

type PebbleEngine struct {
	db     *pebble.DB
	config dtypes.PebbleBlockDBConfig
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
		db:     db,
		config: config,
	}, nil
}

// GetDB returns the underlying pebble database for metrics collection.
func (e *PebbleEngine) GetDB() *pebble.DB {
	return e.db
}

func (e *PebbleEngine) Close() error {
	return e.db.Close()
}

// makeKey creates a key for the given root and block type.
func makeKey(root []byte, blockType uint16) []byte {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], blockType)
	return key
}

func makeKeyRange(root []byte) ([]byte, []byte) {
	start := makeKey(root, 0)
	end := makeKey(root, math.MaxUint16)
	return start, end
}

// getComponent retrieves a single component from the database.
// Returns (data, version, timestamp, error). Returns nil data if not found.
func (e *PebbleEngine) getComponent(root []byte, blockType uint16) ([]byte, uint64, time.Time, error) {
	key := makeKey(root, blockType)

	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, 0, time.Time{}, nil
	}
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	defer closer.Close()

	if len(res) < valueHeaderSize {
		return nil, 0, time.Time{}, nil
	}

	version := binary.BigEndian.Uint64(res[:8])
	timestamp := time.Unix(0, int64(binary.BigEndian.Uint64(res[8:16])))

	data := make([]byte, len(res)-valueHeaderSize)
	copy(data, res[valueHeaderSize:])

	return data, version, timestamp, nil
}

// encodeComponentValue serializes a block component with its version and write timestamp.
func encodeComponentValue(version uint64, data []byte) []byte {
	value := make([]byte, valueHeaderSize+len(data))
	binary.BigEndian.PutUint64(value[:8], version)
	binary.BigEndian.PutUint64(value[8:16], uint64(time.Now().UnixNano()))
	copy(value[valueHeaderSize:], data)

	return value
}

// getStoredComponents scans the component key range for a block and returns the stored flags.
func (e *PebbleEngine) getStoredComponents(root []byte) (types.BlockDataFlags, error) {
	lowerBound, upperBound := makeKeyRange(root)

	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: upperBound,
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	var flags types.BlockDataFlags
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) < 4 || len(iter.Value()) < valueHeaderSize {
			continue
		}

		switch binary.BigEndian.Uint16(key[len(key)-2:]) {
		case BlockTypeHeader:
			flags |= types.BlockDataFlagHeader
		case BlockTypeBody:
			flags |= types.BlockDataFlagBody
		case BlockTypePayload:
			flags |= types.BlockDataFlagPayload
		case BlockTypeBal:
			flags |= types.BlockDataFlagBal
		}

		if flags == types.BlockDataFlagAll {
			break
		}
	}

	return flags, iter.Error()
}

// GetStoredComponents returns which components exist for a block.
func (e *PebbleEngine) GetStoredComponents(_ context.Context, _ uint64, root []byte) (types.BlockDataFlags, error) {
	return e.getStoredComponents(root)
}

// GetBlock retrieves block data with selective loading based on flags.
// Note: LRU access tracking should be done by the caller via CacheCleanup.RecordAccess()
// to avoid expensive read-modify-write operations on every access.
func (e *PebbleEngine) GetBlock(
	_ context.Context,
	_ uint64,
	root []byte,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	blockData := &types.BlockData{}

	// Load header if requested
	if flags.Has(types.BlockDataFlagHeader) {
		data, version, _, err := e.getComponent(root, BlockTypeHeader)
		if err != nil {
			return nil, fmt.Errorf("failed to get header: %w", err)
		}
		if data != nil {
			blockData.HeaderVersion = version
			blockData.HeaderData = data
		}
	}

	// Load body if requested
	if flags.Has(types.BlockDataFlagBody) {
		data, version, _, err := e.getComponent(root, BlockTypeBody)
		if err != nil {
			return nil, fmt.Errorf("failed to get body: %w", err)
		}

		if data != nil {
			blockData.BodyVersion = version
			if parseBlock != nil {
				body, err := parseBlock(version, data)
				if err != nil {
					return nil, fmt.Errorf("failed to parse body: %w", err)
				}
				blockData.Body = body
			} else {
				blockData.BodyData = data
			}
		}
	}

	// Load payload if requested
	if flags.Has(types.BlockDataFlagPayload) {
		data, version, _, err := e.getComponent(root, BlockTypePayload)
		if err != nil {
			return nil, fmt.Errorf("failed to get payload: %w", err)
		}

		if data != nil {
			blockData.PayloadVersion = version
			if parsePayload != nil {
				payload, err := parsePayload(version, data)
				if err != nil {
					return nil, fmt.Errorf("failed to parse payload: %w", err)
				}
				blockData.Payload = payload
			} else {
				blockData.PayloadData = data
			}
		}
	}

	// Load BAL if requested
	if flags.Has(types.BlockDataFlagBal) {
		data, version, _, err := e.getComponent(root, BlockTypeBal)
		if err != nil {
			return nil, fmt.Errorf("failed to get BAL: %w", err)
		}

		if data != nil {
			blockData.BalVersion = version
			blockData.BalData = data
		}
	}

	return blockData, nil
}

// AddBlock stores block data. Returns (added, updated, error).
// - added: true if a new block was created
// - updated: true if an existing block was updated with new components
func (e *PebbleEngine) AddBlock(
	_ context.Context,
	_ uint64,
	root []byte,
	dataCb func() (*types.BlockData, error),
) (bool, bool, error) {
	// Check what components already exist
	existingFlags, err := e.getStoredComponents(root)
	if err != nil {
		return false, false, fmt.Errorf("failed to check existing components: %w", err)
	}

	// Get the new data
	blockData, err := dataCb()
	if err != nil {
		return false, false, err
	}

	// Determine what new components we have
	newFlags := types.StoredFlagsFromBlockData(blockData)

	// Calculate components to add (new components not in existing)
	toAdd := newFlags &^ existingFlags

	if toAdd == 0 {
		// Nothing new to add
		return false, false, nil
	}

	isNew := existingFlags == 0
	isUpdated := !isNew

	batch := e.db.NewBatch()
	defer batch.Close()

	// Store new components
	if toAdd.Has(types.BlockDataFlagHeader) {
		if err := batch.Set(makeKey(root, BlockTypeHeader), encodeComponentValue(blockData.HeaderVersion, blockData.HeaderData), nil); err != nil {
			return false, false, fmt.Errorf("failed to store header: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagBody) {
		if err := batch.Set(makeKey(root, BlockTypeBody), encodeComponentValue(blockData.BodyVersion, blockData.BodyData), nil); err != nil {
			return false, false, fmt.Errorf("failed to store body: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagPayload) {
		if err := batch.Set(makeKey(root, BlockTypePayload), encodeComponentValue(blockData.PayloadVersion, blockData.PayloadData), nil); err != nil {
			return false, false, fmt.Errorf("failed to store payload: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagBal) {
		if err := batch.Set(makeKey(root, BlockTypeBal), encodeComponentValue(blockData.BalVersion, blockData.BalData), nil); err != nil {
			return false, false, fmt.Errorf("failed to store BAL: %w", err)
		}
	}

	if err := batch.Commit(nil); err != nil {
		return false, false, fmt.Errorf("failed to commit block components: %w", err)
	}

	return isNew, isUpdated, nil
}

// GetConfig returns the engine configuration.
func (e *PebbleEngine) GetConfig() dtypes.PebbleBlockDBConfig {
	return e.config
}
