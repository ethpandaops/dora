package pebble

import (
	"context"
	"encoding/binary"
	"fmt"
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

// setComponent stores a single component in the database.
func (e *PebbleEngine) setComponent(root []byte, blockType uint16, version uint64, data []byte) error {
	key := makeKey(root, blockType)

	value := make([]byte, valueHeaderSize+len(data))
	binary.BigEndian.PutUint64(value[:8], version)
	binary.BigEndian.PutUint64(value[8:16], uint64(time.Now().UnixNano()))
	copy(value[valueHeaderSize:], data)

	return e.db.Set(key, value, nil)
}

// componentExists checks if a component exists in the database.
func (e *PebbleEngine) componentExists(root []byte, blockType uint16) bool {
	key := makeKey(root, blockType)

	res, closer, err := e.db.Get(key)
	if err == nil && len(res) >= valueHeaderSize {
		closer.Close()
		return true
	}
	return false
}

// GetStoredComponents returns which components exist for a block.
func (e *PebbleEngine) GetStoredComponents(_ context.Context, _ uint64, root []byte) (types.BlockDataFlags, error) {
	var flags types.BlockDataFlags

	if e.componentExists(root, BlockTypeHeader) {
		flags |= types.BlockDataFlagHeader
	}
	if e.componentExists(root, BlockTypeBody) {
		flags |= types.BlockDataFlagBody
	}
	if e.componentExists(root, BlockTypePayload) {
		flags |= types.BlockDataFlagPayload
	}
	if e.componentExists(root, BlockTypeBal) {
		flags |= types.BlockDataFlagBal
	}

	return flags, nil
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
	existingFlags, err := e.GetStoredComponents(context.Background(), 0, root)
	if err != nil {
		return false, false, fmt.Errorf("failed to check existing components: %w", err)
	}

	// Get the new data
	blockData, err := dataCb()
	if err != nil {
		return false, false, err
	}

	// Determine what new components we have
	var newFlags types.BlockDataFlags
	if len(blockData.HeaderData) > 0 {
		newFlags |= types.BlockDataFlagHeader
	}
	if len(blockData.BodyData) > 0 {
		newFlags |= types.BlockDataFlagBody
	}
	if blockData.PayloadVersion != 0 && len(blockData.PayloadData) > 0 {
		newFlags |= types.BlockDataFlagPayload
	}
	if blockData.BalVersion != 0 && len(blockData.BalData) > 0 {
		newFlags |= types.BlockDataFlagBal
	}

	// Calculate components to add (new components not in existing)
	toAdd := newFlags &^ existingFlags

	if toAdd == 0 {
		// Nothing new to add
		return false, false, nil
	}

	isNew := existingFlags == 0
	isUpdated := !isNew

	// Store new components
	if toAdd.Has(types.BlockDataFlagHeader) {
		if err := e.setComponent(root, BlockTypeHeader, blockData.HeaderVersion, blockData.HeaderData); err != nil {
			return false, false, fmt.Errorf("failed to store header: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagBody) {
		if err := e.setComponent(root, BlockTypeBody, blockData.BodyVersion, blockData.BodyData); err != nil {
			return false, false, fmt.Errorf("failed to store body: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagPayload) {
		if err := e.setComponent(root, BlockTypePayload, blockData.PayloadVersion, blockData.PayloadData); err != nil {
			return false, false, fmt.Errorf("failed to store payload: %w", err)
		}
	}

	if toAdd.Has(types.BlockDataFlagBal) {
		if err := e.setComponent(root, BlockTypeBal, blockData.BalVersion, blockData.BalData); err != nil {
			return false, false, fmt.Errorf("failed to store BAL: %w", err)
		}
	}

	return isNew, isUpdated, nil
}

// GetDB returns the underlying Pebble database for cleanup operations.
func (e *PebbleEngine) GetDB() *pebble.DB {
	return e.db
}

// GetConfig returns the engine configuration.
func (e *PebbleEngine) GetConfig() dtypes.PebbleBlockDBConfig {
	return e.config
}
