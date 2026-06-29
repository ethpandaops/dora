package pebble

import (
	"encoding/binary"
	"time"

	"github.com/cockroachdb/pebble"
)

// Access-tracking records (KeyNamespaceAccessTrack, see pebble.go) hold
// last-access timestamps for slot-keyed cache entities (exec data, duties) so
// they can be evicted LRU-style. Block components track access in the ns2 LRU
// record (see cleanup.go) instead.
const (
	// execEntityTailLen identifies one exec-data object: [slot:8][blockHash:4].
	execEntityTailLen = execDataKeyLen - 2
	// dutiesEntityTailLen groups duties at epoch granularity: [firstSlot:8].
	// An epoch is stored as several keys sharing this prefix; they are evicted
	// together so a partial epoch never remains.
	dutiesEntityTailLen = 8
)

// makeAccessKey builds an access-record key for a slot-keyed entity.
// Format: [accessNs:2][entityNs:2][slot:8][tail...] -> [accessUnixNano:8].
// The entity's slot is the first 8 bytes of the tail, so access records share
// the slot ordering of the data they track and can be range-deleted by slot.
func makeAccessKey(entityNs uint16, entityTail []byte) []byte {
	key := make([]byte, 4+len(entityTail))
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceAccessTrack)
	binary.BigEndian.PutUint16(key[2:4], entityNs)
	copy(key[4:], entityTail)
	return key
}

// makeNamespaceBounds returns the [lower, upper) key bounds covering an entire
// namespace (upper is the start of the next namespace, an exclusive bound).
func makeNamespaceBounds(ns uint16) ([]byte, []byte) {
	return makeNamespaceRangeStart(ns), makeNamespaceRangeStart(ns + 1)
}

// makeAccessRangeStart returns the range start key for an entity namespace's
// access records.
func makeAccessRangeStart(entityNs uint16) []byte {
	key := make([]byte, 4)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceAccessTrack)
	binary.BigEndian.PutUint16(key[2:4], entityNs)
	return key
}

// makeAccessSlotKey returns [accessNs:2][entityNs:2][slot:8] for range operations.
func makeAccessSlotKey(entityNs uint16, slot uint64) []byte {
	key := make([]byte, 12)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceAccessTrack)
	binary.BigEndian.PutUint16(key[2:4], entityNs)
	binary.BigEndian.PutUint64(key[4:12], slot)
	return key
}

// execAccessTail builds the entity tail for an exec-data object.
func execAccessTail(slot uint64, blockHash []byte) []byte {
	tail := make([]byte, execEntityTailLen)
	binary.BigEndian.PutUint64(tail[0:8], slot)
	copyHashPrefix(tail[8:12], blockHash, 4)
	return tail
}

// dutiesAccessTail builds the entity tail for an epoch's duties.
func dutiesAccessTail(firstSlot uint64) []byte {
	tail := make([]byte, dutiesEntityTailLen)
	binary.BigEndian.PutUint64(tail[0:8], firstSlot)
	return tail
}

// getAccessTime reads the last-access timestamp (Unix nano) for an access key.
// Returns 0 if never recorded.
func (c *CacheCleanup) getAccessTime(db *pebble.DB, accessKey []byte) int64 {
	res, closer, err := db.Get(accessKey)
	if err != nil {
		return 0
	}
	defer func() { _ = closer.Close() }()

	if len(res) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(res[0:8]))
}

// execLRUEnabled reports whether exec-data cache eviction uses LRU (so accesses
// must be tracked).
func (c *CacheCleanup) execLRUEnabled() bool {
	return c.config.ExecDataRetention.Enabled && c.config.ExecDataRetention.CleanupMode == "lru"
}

// dutiesLRUEnabled reports whether duties cache eviction uses LRU.
func (c *CacheCleanup) dutiesLRUEnabled() bool {
	return c.config.DutiesRetention.Enabled && c.config.DutiesRetention.CleanupMode == "lru"
}

// RecordExecAccess buffers a last-access update for an exec-data object. No-op
// unless exec-data eviction is in LRU mode.
func (c *CacheCleanup) RecordExecAccess(slot uint64, blockHash []byte) {
	if !c.execLRUEnabled() {
		return
	}
	c.recordAccess(makeAccessKey(KeyNamespaceExecData, execAccessTail(slot, blockHash)))
}

// RecordDutiesAccess buffers a last-access update for an epoch's duties. No-op
// unless duties eviction is in LRU mode.
func (c *CacheCleanup) RecordDutiesAccess(firstSlot uint64) {
	if !c.dutiesLRUEnabled() {
		return
	}
	c.recordAccess(makeAccessKey(KeyNamespaceDuties, dutiesAccessTail(firstSlot)))
}

// recordAccess buffers an access-record write, flushing when the buffer is full.
func (c *CacheCleanup) recordAccess(accessKey []byte) {
	c.lruMu.Lock()
	defer c.lruMu.Unlock()

	if c.accessBuffer == nil {
		c.accessBuffer = make(map[string]int64, 100)
	}
	c.accessBuffer[string(accessKey)] = time.Now().UnixNano()

	if len(c.accessBuffer) >= maxLRUBufferSize {
		c.flushLRULocked()
	}
}
