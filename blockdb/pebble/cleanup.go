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
	// KeyNamespaceLRU is the namespace for LRU tracking data.
	KeyNamespaceLRU uint16 = 2

	// LRU value format: [headerAccess (8B)] [bodyAccess (8B)] [payloadAccess (8B)] [balAccess (8B)]
	// Each access time is a Unix nanosecond timestamp, 0 means never accessed.
	lruValueSize = 32

	// Maximum number of LRU updates to buffer before forcing a flush.
	maxLRUBufferSize = 1000
)

// CacheCleanup manages background cleanup of cached data.
type CacheCleanup struct {
	engine *PebbleEngine
	config dtypes.PebbleBlockDBConfig
	logger logrus.FieldLogger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// LRU update buffer
	lruMu     sync.Mutex
	lruBuffer map[string]*lruUpdate // root hex -> update
}

// lruUpdate holds pending LRU timestamp updates for a block.
type lruUpdate struct {
	root          []byte
	headerAccess  int64 // Unix nano, 0 = no update
	bodyAccess    int64
	payloadAccess int64
	balAccess     int64
}

// NewCacheCleanup creates a new cache cleanup manager.
func NewCacheCleanup(engine *PebbleEngine, logger logrus.FieldLogger) *CacheCleanup {
	ctx, cancel := context.WithCancel(context.Background())

	return &CacheCleanup{
		engine:    engine,
		config:    engine.GetConfig(),
		logger:    logger.WithField("component", "pebble-cleanup"),
		ctx:       ctx,
		cancel:    cancel,
		lruBuffer: make(map[string]*lruUpdate, 100),
	}
}

// Start begins the background cleanup loop.
func (c *CacheCleanup) Start() {
	if c.config.CleanupInterval == 0 {
		c.logger.Info("cleanup disabled (interval is 0)")
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

// flushLRULocked flushes LRU buffer (must hold lruMu).
func (c *CacheCleanup) flushLRULocked() {
	if len(c.lruBuffer) == 0 {
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

	if err := batch.Commit(nil); err != nil {
		c.logger.Errorf("failed to flush LRU updates: %v", err)
	}
	batch.Close()

	// Clear buffer
	c.lruBuffer = make(map[string]*lruUpdate, 100)
}

// makeLRUKey creates the key for LRU data.
func makeLRUKey(root []byte) []byte {
	key := make([]byte, 2+len(root))
	binary.BigEndian.PutUint16(key[:2], KeyNamespaceLRU)
	copy(key[2:], root)
	return key
}

// runCleanup performs cleanup for all configured component types.
func (c *CacheCleanup) runCleanup() {
	c.logger.Debug("starting cache cleanup")

	componentConfigs := map[uint16]*dtypes.BlockDbRetentionConfig{
		BlockTypeHeader:  &c.config.HeaderRetention,
		BlockTypeBody:    &c.config.BodyRetention,
		BlockTypePayload: &c.config.PayloadRetention,
		BlockTypeBal:     &c.config.BalRetention,
	}

	for blockType, config := range componentConfigs {
		if config == nil || !config.Enabled {
			continue
		}

		switch config.CleanupMode {
		case "age":
			c.cleanupByAge(blockType, config.RetentionTime)
		case "lru":
			c.cleanupByLRU(blockType, config.MaxSize*1024*1024) // Convert MB to bytes
		}
	}
}

// cleanupByAge removes entries older than the retention time based on storage timestamp.
func (c *CacheCleanup) cleanupByAge(blockType uint16, retention time.Duration) {
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

		// Check if this key is in the block namespace
		if len(key) < 36 { // 2 (namespace) + 32 (root) + 2 (type)
			continue
		}

		namespace := binary.BigEndian.Uint16(key[:2])
		if namespace != KeyNamespaceBlock {
			continue
		}

		keyType := binary.BigEndian.Uint16(key[len(key)-2:])
		if keyType != blockType {
			continue
		}

		// Check timestamp from value (stored at offset 8)
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
			c.logger.Errorf("failed to commit age cleanup batch: %v", err)
		} else {
			c.logger.Infof("cleaned up %d entries for block type %d (age-based)", deleted, blockType)
		}
	}
}

// lruEntry represents an entry for LRU cleanup sorting.
type lruEntry struct {
	root       []byte
	key        []byte
	size       int64
	lastAccess int64
}

// cleanupByLRU removes least recently used entries when size exceeds limit.
func (c *CacheCleanup) cleanupByLRU(blockType uint16, maxSize int64) {
	if maxSize == 0 {
		return
	}

	db := c.engine.GetDB()

	// First pass: collect all entries with their sizes and LRU timestamps
	entries := make([]*lruEntry, 0, 1000)
	var totalSize int64

	iter, err := db.NewIter(&pebble.IterOptions{})
	if err != nil {
		c.logger.Errorf("failed to create iterator: %v", err)
		return
	}

	// Scan block entries
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()

		if len(key) < 36 {
			continue
		}

		namespace := binary.BigEndian.Uint16(key[:2])
		if namespace != KeyNamespaceBlock {
			continue
		}

		keyType := binary.BigEndian.Uint16(key[len(key)-2:])
		if keyType != blockType {
			continue
		}

		// Extract root from key
		root := key[2 : len(key)-2]
		value := iter.Value()
		size := int64(len(value))
		totalSize += size

		// Get LRU timestamp for this entry
		lastAccess := c.getLRUTimestamp(db, root, blockType)

		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		rootCopy := make([]byte, len(root))
		copy(rootCopy, root)

		entries = append(entries, &lruEntry{
			root:       rootCopy,
			key:        keyCopy,
			size:       size,
			lastAccess: lastAccess,
		})
	}
	iter.Close()

	// Check if we need to clean up
	if totalSize <= maxSize {
		return
	}

	// Sort by last access time (oldest first, 0 = never accessed = oldest)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastAccess < entries[j].lastAccess
	})

	// Delete oldest entries until we're under the limit
	batch := db.NewBatch()
	defer batch.Close()

	deleted := 0
	freedSize := int64(0)
	targetFree := totalSize - maxSize

	for _, entry := range entries {
		if freedSize >= targetFree {
			break
		}

		batch.Delete(entry.key, nil)
		freedSize += entry.size
		deleted++
	}

	if deleted > 0 {
		if err := batch.Commit(nil); err != nil {
			c.logger.Errorf("failed to commit LRU cleanup batch: %v", err)
		} else {
			c.logger.Infof("cleaned up %d entries for block type %d (LRU-based, freed %d bytes)",
				deleted, blockType, freedSize)
		}
	}
}

// getLRUTimestamp retrieves the LRU timestamp for a specific component.
func (c *CacheCleanup) getLRUTimestamp(db *pebble.DB, root []byte, blockType uint16) int64 {
	key := makeLRUKey(root)

	res, closer, err := db.Get(key)
	if err != nil {
		return 0 // Never accessed
	}
	defer closer.Close()

	if len(res) < lruValueSize {
		return 0
	}

	// Extract timestamp based on block type
	var offset int
	switch blockType {
	case BlockTypeHeader:
		offset = 0
	case BlockTypeBody:
		offset = 8
	case BlockTypePayload:
		offset = 16
	case BlockTypeBal:
		offset = 24
	default:
		return 0
	}

	return int64(binary.BigEndian.Uint64(res[offset : offset+8]))
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
