package blockdb

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb/pebble"
	"github.com/ethpandaops/dora/blockdb/s3"
	"github.com/ethpandaops/dora/blockdb/tiered"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

// BlockDb is the main wrapper for block database operations.
type BlockDb struct {
	engine       types.BlockDbEngine
	execEngine   types.ExecDataEngine // nil if engine doesn't support exec data
	dutiesEngine types.DutiesEngine   // nil if engine doesn't support duties storage

	txHashIndex       types.TxHashIndex // nil until detected natively or injected
	txHashIndexNative bool              // true if provided by the engine (write post-commit), false if a relational adapter (write in-tx)
}

// GlobalBlockDb is the global block database instance.
var GlobalBlockDb *BlockDb

// SetTimeToSlotFn forwards a time->slot resolver to the engine when it has a
// cache that needs one for age-based eviction (currently the tiered engine).
// No-op for engines without such a cache.
func (db *BlockDb) SetTimeToSlotFn(fn func(t time.Time) uint64) {
	if db == nil || db.engine == nil {
		return
	}
	if s, ok := db.engine.(interface {
		SetTimeToSlotFn(func(t time.Time) uint64)
	}); ok {
		s.SetTimeToSlotFn(fn)
	}
}

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
	if dutiesEngine, ok := engine.(types.DutiesEngine); ok {
		db.dutiesEngine = dutiesEngine
	}
	if txHashIndex, ok := engine.(types.TxHashIndex); ok {
		db.txHashIndex = txHashIndex
		db.txHashIndexNative = true
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
	if dutiesEngine, ok := engine.(types.DutiesEngine); ok {
		db.dutiesEngine = dutiesEngine
	}
	if txHashIndex, ok := engine.(types.TxHashIndex); ok {
		db.txHashIndex = txHashIndex
		db.txHashIndexNative = true
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
	if dutiesEngine, ok := engine.(types.DutiesEngine); ok {
		db.dutiesEngine = dutiesEngine
	}
	if txHashIndex, ok := engine.(types.TxHashIndex); ok {
		db.txHashIndex = txHashIndex
		db.txHashIndexNative = true
	}

	GlobalBlockDb = db

	return nil
}

// GetEngine returns the underlying storage engine.
func (db *BlockDb) GetEngine() types.BlockDbEngine {
	return db.engine
}

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

// SetTxHashIndex injects a tx-hash index implementation (e.g. the relational
// adapter) and marks it non-native. Overrides any natively-detected index.
func (db *BlockDb) SetTxHashIndex(idx types.TxHashIndex) {
	db.txHashIndex = idx
	db.txHashIndexNative = false
}

// SupportsTxHashIndex returns true if a tx-hash index is available.
func (db *BlockDb) SupportsTxHashIndex() bool {
	return db.txHashIndex != nil
}

// TxHashIndexNative reports whether the active index is engine-native (Pebble/
// Tiered, written post-commit) rather than the relational adapter (written
// in-transaction with el_transactions).
func (db *BlockDb) TxHashIndexNative() bool {
	return db.txHashIndexNative
}

// PutTxHashes writes tx-hash index entries.
func (db *BlockDb) PutTxHashes(ctx context.Context, entries []types.TxHashEntry) error {
	if db.txHashIndex == nil {
		return fmt.Errorf("tx-hash index not available")
	}
	return db.txHashIndex.PutTxHashes(ctx, entries)
}

// LookupTxHash returns candidate tx_uids for an exact 10-byte prefix.
func (db *BlockDb) LookupTxHash(ctx context.Context, prefix []byte) ([]uint64, error) {
	if db.txHashIndex == nil {
		return nil, nil
	}
	return db.txHashIndex.LookupTxHash(ctx, prefix)
}

// LookupTxHashRange returns candidate tx_uids for prefixes in [lo, hi).
func (db *BlockDb) LookupTxHashRange(ctx context.Context, lo, hi []byte) ([]uint64, error) {
	if db.txHashIndex == nil {
		return nil, nil
	}
	return db.txHashIndex.LookupTxHashRange(ctx, lo, hi)
}

// PruneTxHashBefore prunes tx-hash index entries for slots before maxSlot.
func (db *BlockDb) PruneTxHashBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	if db.txHashIndex == nil {
		return 0, nil
	}
	return db.txHashIndex.PruneTxHashBefore(ctx, maxSlot)
}

// TxHashIndexDiskUsage returns the approximate on-disk size of a native
// (engine-backed) tx-hash index, or 0 when the index is relational or absent.
func (db *BlockDb) TxHashIndexDiskUsage() int64 {
	if db.txHashIndex == nil || !db.txHashIndexNative {
		return 0
	}
	if est, ok := db.engine.(interface{ EstimateTxHashIndexSize() int64 }); ok {
		return est.EstimateTxHashIndexSize()
	}
	return 0
}

// SupportsDuties returns true if the underlying engine supports duties storage.
func (db *BlockDb) SupportsDuties() bool {
	return db.dutiesEngine != nil
}

// AddEpochDuties stores the resolved per-epoch duties. Returns stored size.
func (db *BlockDb) AddEpochDuties(ctx context.Context, duties *types.EpochDuties) (int64, error) {
	if db.dutiesEngine == nil {
		return 0, fmt.Errorf("duties storage not supported by engine")
	}
	return db.dutiesEngine.AddEpochDuties(ctx, duties)
}

// GetEpochDuties retrieves the full resolved duties for an epoch.
func (db *BlockDb) GetEpochDuties(ctx context.Context, firstSlot uint64) (*types.EpochDuties, error) {
	if db.dutiesEngine == nil {
		return nil, nil
	}
	return db.dutiesEngine.GetEpochDuties(ctx, firstSlot)
}

// GetSlotCommittees retrieves the attester committees for a single slot.
func (db *BlockDb) GetSlotCommittees(ctx context.Context, firstSlot uint64, slot uint64) ([][]uint64, error) {
	if db.dutiesEngine == nil {
		return nil, nil
	}
	return db.dutiesEngine.GetSlotCommittees(ctx, firstSlot, slot)
}

// GetSlotPtc retrieves the PTC members for a single slot.
func (db *BlockDb) GetSlotPtc(ctx context.Context, firstSlot uint64, slot uint64) ([]uint64, error) {
	if db.dutiesEngine == nil {
		return nil, nil
	}
	return db.dutiesEngine.GetSlotPtc(ctx, firstSlot, slot)
}

// HasEpochDuties checks if duties exist for an epoch.
func (db *BlockDb) HasEpochDuties(ctx context.Context, firstSlot uint64) (bool, error) {
	if db.dutiesEngine == nil {
		return false, nil
	}
	return db.dutiesEngine.HasEpochDuties(ctx, firstSlot)
}

// PruneEpochDutiesBefore deletes duties objects for all epochs whose first slot is before maxFirstSlot.
func (db *BlockDb) PruneEpochDutiesBefore(ctx context.Context, maxFirstSlot uint64) (int64, error) {
	if db.dutiesEngine == nil {
		return 0, nil
	}
	return db.dutiesEngine.PruneEpochDutiesBefore(ctx, maxFirstSlot)
}
