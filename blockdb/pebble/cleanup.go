package pebble

import (
	"context"
	"encoding/binary"
	"sort"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
)

const (
	// LRU value format: [headerAccess (8B)] [bodyAccess (8B)] [payloadAccess (8B)] [balAccess (8B)]
	// Each access time is a Unix nanosecond timestamp, 0 means never accessed.
	// Stored under KeyNamespaceLRU (see pebble.go).
	lruValueSize = 32

	// Maximum number of LRU updates to buffer before forcing a flush.
	maxLRUBufferSize = 1000

	// defaultCleanupInterval is used when no cleanupInterval is configured.
	// A negative configured interval disables the cleanup loop entirely.
	defaultCleanupInterval = 12 * time.Hour
)

// CacheCleanup manages background cleanup of cached data.
type CacheCleanup struct {
	engine *PebbleEngine
	config dtypes.PebbleBlockDBConfig
	logger logrus.FieldLogger

	// cacheMode is true when Pebble is a cache in front of S3 (tiered): eviction
	// removes cached copies only, and exec data is cache-evictable. When false
	// (Pebble is the authoritative store), the same retentions instead delete the
	// data, and exec-data retention is left to the EL indexer (see runCleanup).
	cacheMode bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// timeToSlotFn resolves a wall-clock time to a slot number, used to derive
	// the cutoff slot for age-based eviction of slot-keyed namespaces (exec
	// data, duties). Nil until set via SetTimeToSlotFn; age eviction of those
	// namespaces is skipped while nil.
	timeToSlotFn func(t time.Time) uint64

	// LRU update buffers (guarded by lruMu)
	lruMu        sync.Mutex
	lruBuffer    map[string]*lruUpdate // root hex -> block-component access update
	accessBuffer map[string]int64      // access-record key -> Unix nano (exec/duties)
}

// SetTimeToSlotFn installs the time->slot resolver used by age-based eviction of
// the slot-keyed namespaces. Safe to call once after construction, before the
// cleanup loop needs it.
func (c *CacheCleanup) SetTimeToSlotFn(fn func(t time.Time) uint64) {
	c.timeToSlotFn = fn
}

// lruUpdate holds pending LRU timestamp updates for a block.
type lruUpdate struct {
	root          []byte
	headerAccess  int64 // Unix nano, 0 = no update
	bodyAccess    int64
	payloadAccess int64
	balAccess     int64
}

// NewCacheCleanup creates a new cleanup manager. cacheMode selects tiered
// (cache eviction) vs pebble (authoritative deletion) semantics; see runCleanup.
func NewCacheCleanup(engine *PebbleEngine, logger logrus.FieldLogger, cacheMode bool) *CacheCleanup {
	ctx, cancel := context.WithCancel(context.Background())

	config := engine.GetConfig()
	if config.CleanupInterval == 0 {
		config.CleanupInterval = defaultCleanupInterval
	}

	return &CacheCleanup{
		engine:    engine,
		config:    config,
		logger:    logger.WithField("component", "pebble-cleanup"),
		cacheMode: cacheMode,
		ctx:       ctx,
		cancel:    cancel,
		lruBuffer: make(map[string]*lruUpdate, 100),
	}
}

// Start begins the background cleanup loop.
func (c *CacheCleanup) Start() {
	if c.config.CleanupInterval < 0 {
		c.logger.Info("cleanup disabled (negative interval)")
		return
	}

	c.wg.Add(1)
	go c.runCleanupLoop()
}

// Stop stops the background cleanup loop.
func (c *CacheCleanup) Stop() {
	c.cancel()
	c.wg.Wait()

	// Final flush of LRU buffer
	c.FlushLRU()
}

// runCleanupLoop runs the periodic cleanup.
func (c *CacheCleanup) runCleanupLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.FlushLRU()
			c.runCleanup()
		}
	}
}

// RecordAccess records an access for LRU tracking. Buffered until flush.
func (c *CacheCleanup) RecordAccess(root []byte, flags types.BlockDataFlags) {
	c.lruMu.Lock()
	defer c.lruMu.Unlock()

	key := string(root)
	now := time.Now().UnixNano()

	update, exists := c.lruBuffer[key]
	if !exists {
		rootCopy := make([]byte, len(root))
		copy(rootCopy, root)
		update = &lruUpdate{root: rootCopy}
		c.lruBuffer[key] = update
	}

	if flags.Has(types.BlockDataFlagHeader) {
		update.headerAccess = now
	}
	if flags.Has(types.BlockDataFlagBody) {
		update.bodyAccess = now
	}
	if flags.Has(types.BlockDataFlagPayload) {
		update.payloadAccess = now
	}
	if flags.Has(types.BlockDataFlagBal) {
		update.balAccess = now
	}

	// Force flush if buffer is too large
	if len(c.lruBuffer) >= maxLRUBufferSize {
		c.flushLRULocked()
	}
}

// FlushLRU flushes buffered LRU updates to Pebble.
func (c *CacheCleanup) FlushLRU() {
	c.lruMu.Lock()
	defer c.lruMu.Unlock()
	c.flushLRULocked()
}

// flushLRULocked flushes the block-component LRU buffer and the slot-keyed
// access-record buffer (must hold lruMu).
func (c *CacheCleanup) flushLRULocked() {
	if len(c.lruBuffer) == 0 && len(c.accessBuffer) == 0 {
		return
	}

	db := c.engine.GetDB()
	batch := db.NewBatch()

	for _, update := range c.lruBuffer {
		key := makeLRUKey(update.root)

		// Read existing LRU data
		existing := make([]byte, lruValueSize)
		if res, closer, err := db.Get(key); err == nil {
			if len(res) >= lruValueSize {
				copy(existing, res)
			}
			closer.Close()
		}

		// Merge updates (only update non-zero values)
		value := make([]byte, lruValueSize)
		copy(value, existing)

		if update.headerAccess > 0 {
			binary.BigEndian.PutUint64(value[0:8], uint64(update.headerAccess))
		}
		if update.bodyAccess > 0 {
			binary.BigEndian.PutUint64(value[8:16], uint64(update.bodyAccess))
		}
		if update.payloadAccess > 0 {
			binary.BigEndian.PutUint64(value[16:24], uint64(update.payloadAccess))
		}
		if update.balAccess > 0 {
			binary.BigEndian.PutUint64(value[24:32], uint64(update.balAccess))
		}

		batch.Set(key, value, nil)
	}

	for accessKey, ts := range c.accessBuffer {
		value := make([]byte, 8)
		binary.BigEndian.PutUint64(value, uint64(ts))
		batch.Set([]byte(accessKey), value, nil)
	}

	if err := batch.Commit(nil); err != nil {
		c.logger.Errorf("failed to flush LRU updates: %v", err)
	}
	batch.Close()

	// Clear buffers
	c.lruBuffer = make(map[string]*lruUpdate, 100)
	c.accessBuffer = make(map[string]int64, 100)
}

// makeLRUKey creates the key for LRU data.
func makeLRUKey(root []byte) []byte {
	key := make([]byte, 2+len(root))
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceLRU)
	copy(key[2:], root)
	return key
}

// runCleanup applies the per-namespace retention configs. In tiered mode it
// evicts cached copies (the authoritative data stays in S3); in pebble mode the
// Pebble store is authoritative, so the same operation deletes the data. Either
// way, block and duties data are governed here. Exec data is only handled in
// cache (tiered) mode — in pebble mode its retention is owned by the EL indexer
// (executionIndexer.detailsRetention), so blockDb exec-data retention is ignored
// (a warning is logged at init). The tx-hash index namespaces (4/5) are never
// touched here — they are pruned by the EL details retention.
func (c *CacheCleanup) runCleanup() {
	c.logger.Debug("starting blockdb cleanup")

	// Block components (header/body/payload/bal) are cleared together per block.
	c.cleanupBlocks(&c.config.BlockRetention)
	c.cleanupSlotNamespace(KeyNamespaceDuties, DutiesKeyLen, dutiesEntityTailLen, &c.config.DutiesRetention)

	// Exec data is only cache-evictable in tiered mode; in pebble mode it is
	// authoritative and managed by the EL indexer's details retention.
	if c.cacheMode {
		c.cleanupSlotNamespace(KeyNamespaceExecData, execDataKeyLen, execEntityTailLen, &c.config.ExecDataRetention)
	}
}

// cleanupBlocks evicts block-component data (namespace 1) by the configured mode.
func (c *CacheCleanup) cleanupBlocks(config *dtypes.BlockDbRetentionConfig) {
	if config == nil || !config.Enabled {
		return
	}

	switch config.CleanupMode {
	case "age":
		c.cleanupBlocksByAge(config.RetentionTime)
	case "lru":
		c.cleanupBlocksByLRU(config.MaxSize * 1024 * 1024) // MB -> bytes
	}
}

// cleanupBlocksByAge removes any block component older than the retention time
// based on its stored write timestamp.
func (c *CacheCleanup) cleanupBlocksByAge(retention time.Duration) {
	if retention == 0 {
		return
	}

	cutoff := time.Now().Add(-retention)
	deleted := 0

	db := c.engine.GetDB()
	iter, err := db.NewIter(&pebble.IterOptions{})
	if err != nil {
		c.logger.Errorf("failed to create iterator: %v", err)
		return
	}
	defer iter.Close()

	batch := db.NewBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()

		if len(key) < 36 { // 2 (namespace) + 32 (root) + 2 (type)
			continue
		}
		if binary.BigEndian.Uint16(key[:2]) != KeyNamespaceBlock {
			continue
		}

		value := iter.Value()
		if len(value) < valueHeaderSize {
			continue
		}

		timestamp := time.Unix(0, int64(binary.BigEndian.Uint64(value[8:16])))
		if timestamp.Before(cutoff) {
			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)
			batch.Delete(keyCopy, nil)
			deleted++
		}
	}

	if deleted > 0 {
		if err := batch.Commit(nil); err != nil {
			c.logger.Errorf("failed to commit block age cleanup batch: %v", err)
		} else {
			c.logger.Infof("cleaned up %d cached block components (age-based)", deleted)
		}
	}
}

// blockEntry aggregates all cached components of a single block (by root) for
// whole-block LRU eviction.
type blockEntry struct {
	keys       [][]byte
	size       int64
	lastAccess int64
}

// cleanupBlocksByLRU evicts whole blocks (all components for a root) least
// recently used first, until the cached block data is under maxSize.
func (c *CacheCleanup) cleanupBlocksByLRU(maxSize int64) {
	if maxSize == 0 {
		return
	}

	db := c.engine.GetDB()

	blocks := make(map[string]*blockEntry)
	order := make([]*blockEntry, 0, 1024)
	var totalSize int64

	lower, upper := makeNamespaceBounds(KeyNamespaceBlock)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		c.logger.Errorf("failed to create iterator: %v", err)
		return
	}

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) < 36 {
			continue
		}

		root := key[2 : len(key)-2]
		id := string(root)
		ent, ok := blocks[id]
		if !ok {
			ent = &blockEntry{lastAccess: c.getBlockLRUTimestamp(db, root)}
			blocks[id] = ent
			order = append(order, ent)
		}

		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		ent.keys = append(ent.keys, keyCopy)
		size := int64(len(key) + len(iter.Value()))
		ent.size += size
		totalSize += size
	}
	iter.Close()

	if totalSize <= maxSize {
		return
	}

	sort.Slice(order, func(i, j int) bool {
		return order[i].lastAccess < order[j].lastAccess
	})

	batch := db.NewBatch()
	defer batch.Close()

	evicted := 0
	freed := int64(0)
	target := totalSize - maxSize

	for _, ent := range order {
		if freed >= target {
			break
		}
		for _, k := range ent.keys {
			batch.Delete(k, nil)
		}
		freed += ent.size
		evicted++
	}

	if evicted > 0 {
		if err := batch.Commit(nil); err != nil {
			c.logger.Errorf("failed to commit block LRU cleanup batch: %v", err)
		} else {
			c.logger.Infof("evicted %d cached blocks (LRU, freed %d bytes)", evicted, freed)
		}
	}
}

// getBlockLRUTimestamp returns the most recent access across a root's cached
// components (0 = never accessed = oldest).
func (c *CacheCleanup) getBlockLRUTimestamp(db *pebble.DB, root []byte) int64 {
	res, closer, err := db.Get(makeLRUKey(root))
	if err != nil {
		return 0
	}
	defer func() { _ = closer.Close() }()

	if len(res) < lruValueSize {
		return 0
	}

	var maxTs int64
	for offset := 0; offset+8 <= lruValueSize; offset += 8 {
		if ts := int64(binary.BigEndian.Uint64(res[offset : offset+8])); ts > maxTs {
			maxTs = ts
		}
	}
	return maxTs
}

// cleanupSlotNamespace evicts a slot-keyed namespace (exec data, duties) from
// the cache by the configured mode. dataKeyLen filters out co-located records
// in a shared namespace (e.g. the ns2 LRU records). entityTailLen is the number
// of key bytes after the 2-byte namespace prefix that identify one entity (so
// an epoch's several duties keys evict together).
func (c *CacheCleanup) cleanupSlotNamespace(ns uint16, dataKeyLen, entityTailLen int, config *dtypes.BlockDbRetentionConfig) {
	if config == nil || !config.Enabled {
		return
	}

	switch config.CleanupMode {
	case "age":
		c.cleanupSlotNamespaceByAge(ns, dataKeyLen, config.RetentionTime)
	case "lru":
		c.cleanupSlotNamespaceByLRU(ns, dataKeyLen, entityTailLen, config.MaxSize*1024*1024)
	}
}

// cleanupSlotNamespaceByAge deletes cached entries whose slot is below the
// cutoff derived from the retention time. Iterates and length-filters rather
// than range-deleting so co-located records in a shared namespace are not hit.
func (c *CacheCleanup) cleanupSlotNamespaceByAge(ns uint16, dataKeyLen int, retention time.Duration) {
	if retention == 0 {
		return
	}
	if c.timeToSlotFn == nil {
		c.logger.Warnf("skipping age cleanup for namespace %d: slot resolver not set", ns)
		return
	}

	cutoffSlot := c.timeToSlotFn(time.Now().Add(-retention))
	if cutoffSlot == 0 {
		return
	}

	db := c.engine.GetDB()
	lower := makeNamespaceRangeStart(ns)
	upper := makeNamespaceSlotKey(ns, cutoffSlot) // slot is the first tail field

	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		c.logger.Errorf("failed to create iterator: %v", err)
		return
	}

	batch := db.NewBatch()
	defer batch.Close()

	deleted := 0
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != dataKeyLen {
			continue // skip co-located records (e.g. ns2 LRU records)
		}
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		batch.Delete(keyCopy, nil)
		deleted++
	}
	iter.Close()

	// Drop the matching access records (their own namespace — range-delete safe).
	if err := db.DeleteRange(makeAccessRangeStart(ns), makeAccessSlotKey(ns, cutoffSlot), pebble.Sync); err != nil {
		c.logger.Debugf("failed to drop access records for namespace %d: %v", ns, err)
	}

	if deleted > 0 {
		if err := batch.Commit(pebble.Sync); err != nil {
			c.logger.Errorf("failed to age-evict namespace %d: %v", ns, err)
		} else {
			c.logger.Infof("cleaned up %d cached entries for namespace %d (age-based)", deleted, ns)
		}
	}
}

// cleanupSlotNamespaceByLRU evicts least-recently-accessed entities of a
// slot-keyed namespace until the cached data is under maxSize.
func (c *CacheCleanup) cleanupSlotNamespaceByLRU(ns uint16, dataKeyLen, entityTailLen int, maxSize int64) {
	if maxSize == 0 {
		return
	}

	db := c.engine.GetDB()

	type slotEntity struct {
		tail       []byte
		keys       [][]byte
		size       int64
		lastAccess int64
	}

	entities := make(map[string]*slotEntity)
	order := make([]*slotEntity, 0, 1024)
	var totalSize int64

	lower, upper := makeNamespaceBounds(ns)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		c.logger.Errorf("failed to create iterator: %v", err)
		return
	}

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != dataKeyLen {
			continue // skip co-located records (e.g. ns2 LRU records)
		}

		tail := key[2 : 2+entityTailLen]
		id := string(tail)
		ent, ok := entities[id]
		if !ok {
			tailCopy := make([]byte, len(tail))
			copy(tailCopy, tail)
			ent = &slotEntity{tail: tailCopy}
			entities[id] = ent
			order = append(order, ent)
		}

		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		ent.keys = append(ent.keys, keyCopy)
		size := int64(len(key) + len(iter.Value()))
		ent.size += size
		totalSize += size
	}
	iter.Close()

	if totalSize <= maxSize {
		return
	}

	for _, ent := range order {
		ent.lastAccess = c.getAccessTime(db, makeAccessKey(ns, ent.tail))
	}

	sort.Slice(order, func(i, j int) bool {
		return order[i].lastAccess < order[j].lastAccess
	})

	batch := db.NewBatch()
	defer batch.Close()

	evicted := 0
	freed := int64(0)
	target := totalSize - maxSize

	for _, ent := range order {
		if freed >= target {
			break
		}
		for _, k := range ent.keys {
			batch.Delete(k, nil)
		}
		batch.Delete(makeAccessKey(ns, ent.tail), nil)
		freed += ent.size
		evicted++
	}

	if evicted > 0 {
		if err := batch.Commit(pebble.Sync); err != nil {
			c.logger.Errorf("failed to commit LRU eviction for namespace %d: %v", ns, err)
		} else {
			c.logger.Infof("evicted %d cached entities for namespace %d (LRU, freed %d bytes)", evicted, ns, freed)
		}
	}
}

// DeleteLRU removes LRU data for a block (call when deleting block data).
func (c *CacheCleanup) DeleteLRU(root []byte) {
	db := c.engine.GetDB()
	key := makeLRUKey(root)
	db.Delete(key, nil)

	// Also remove from buffer
	c.lruMu.Lock()
	delete(c.lruBuffer, string(root))
	c.lruMu.Unlock()
}
