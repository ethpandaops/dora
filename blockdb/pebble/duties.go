package pebble

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/ethpandaops/dora/blockdb/types"
)

// Per-epoch duties (KeyNamespaceDuties, see pebble.go) are stored as several
// keys (one meta key plus one key per slot for committees and PTC) so a single
// slot can be read with a point lookup without loading the whole epoch. Keys are
// grouped by the epoch's first slot to allow efficient range deletion.
const (
	// Duties key record types (stored at byte offset 10).
	DutiesRecordMeta       uint8 = 0 // per-epoch header
	DutiesRecordCommittees uint8 = 1 // per-slot attester committees
	DutiesRecordPtc        uint8 = 2 // per-slot PTC members

	// DutiesKeyLen: [ns:2][firstSlot:8][type:1][slotIndex:2] = 13 bytes
	DutiesKeyLen = 2 + 8 + 1 + 2

	// DivergingDutiesKeyLen: [ns:2][firstSlot:8][depRoot:32][type:1][slotIndex:2]
	// = 45 bytes. Shares the duties namespace with canonical keys but is length-
	// distinct (45 vs 13), so prune range-deletes both while the epoch-count scan
	// only tallies canonical meta keys.
	DivergingDutiesKeyLen = 2 + 8 + 32 + 1 + 2
)

// MakeDutiesKey builds a Pebble key for a duties record. Exported so tooling
// (e.g. the blockdb-copy utility) can read/write duties records directly.
func MakeDutiesKey(firstSlot uint64, recordType uint8, slotIndex uint16) []byte {
	key := make([]byte, DutiesKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceDuties)
	binary.BigEndian.PutUint64(key[2:10], firstSlot)
	key[10] = recordType
	binary.BigEndian.PutUint16(key[11:13], slotIndex)
	return key
}

// MakeDivergingDutiesKey builds a Pebble key for a diverging-fork duties record,
// keyed additionally by the fork's dependent root.
func MakeDivergingDutiesKey(firstSlot uint64, depRoot [32]byte, recordType uint8, slotIndex uint16) []byte {
	key := make([]byte, DivergingDutiesKeyLen)
	binary.BigEndian.PutUint16(key[0:2], KeyNamespaceDuties)
	binary.BigEndian.PutUint64(key[2:10], firstSlot)
	copy(key[10:42], depRoot[:])
	key[42] = recordType
	binary.BigEndian.PutUint16(key[43:45], slotIndex)
	return key
}

// AddEpochDuties stores the duties for an epoch as per-slot keys.
func (e *PebbleEngine) AddEpochDuties(_ context.Context, duties *types.EpochDuties) (int64, error) {
	return e.addDuties(duties, func(recordType uint8, slotIndex uint16) []byte {
		return MakeDutiesKey(duties.FirstSlot, recordType, slotIndex)
	})
}

// AddDivergingEpochDuties stores the duties of a diverging fork, keyed by its
// dependent root alongside the epoch's first slot.
func (e *PebbleEngine) AddDivergingEpochDuties(_ context.Context, duties *types.EpochDuties) (int64, error) {
	depRoot := duties.DependentRoot
	return e.addDuties(duties, func(recordType uint8, slotIndex uint16) []byte {
		return MakeDivergingDutiesKey(duties.FirstSlot, depRoot, recordType, slotIndex)
	})
}

// addDuties writes the meta, per-slot committee and per-slot PTC records for an
// epoch using the provided key builder (canonical or diverging).
func (e *PebbleEngine) addDuties(duties *types.EpochDuties, keyFn func(recordType uint8, slotIndex uint16) []byte) (int64, error) {
	batch := e.db.NewBatch()
	defer func() { _ = batch.Close() }()

	var total int64

	meta := types.EncodeDutiesHeader(duties)
	metaKey := keyFn(DutiesRecordMeta, 0)
	if err := batch.Set(metaKey, meta, nil); err != nil {
		return 0, fmt.Errorf("failed to set duties meta: %w", err)
	}
	total += int64(len(metaKey) + len(meta))

	for slotIndex, committees := range duties.Committees {
		blob, err := types.EncodeSlotCommittees(committees)
		if err != nil {
			return 0, fmt.Errorf("failed to encode slot %d committees: %w", slotIndex, err)
		}
		key := keyFn(DutiesRecordCommittees, uint16(slotIndex))
		if err := batch.Set(key, blob, nil); err != nil {
			return 0, fmt.Errorf("failed to set slot %d committees: %w", slotIndex, err)
		}
		total += int64(len(key) + len(blob))
	}

	if duties.PtcSize > 0 {
		for slotIndex, ptc := range duties.Ptc {
			blob, err := types.EncodeIndexList(ptc)
			if err != nil {
				return 0, fmt.Errorf("failed to encode slot %d ptc: %w", slotIndex, err)
			}
			key := keyFn(DutiesRecordPtc, uint16(slotIndex))
			if err := batch.Set(key, blob, nil); err != nil {
				return 0, fmt.Errorf("failed to set slot %d ptc: %w", slotIndex, err)
			}
			total += int64(len(key) + len(blob))
		}
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, fmt.Errorf("failed to commit duties: %w", err)
	}

	return total, nil
}

// getDutiesValue reads a single duties record value (copied), nil if absent.
func (e *PebbleEngine) getDutiesValue(key []byte) ([]byte, error) {
	res, closer, err := e.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()

	data := make([]byte, len(res))
	copy(data, res)
	return data, nil
}

// GetEpochDuties reads and reassembles the full duties for an epoch.
func (e *PebbleEngine) GetEpochDuties(_ context.Context, firstSlot uint64) (*types.EpochDuties, error) {
	meta, err := e.getDutiesValue(MakeDutiesKey(firstSlot, DutiesRecordMeta, 0))
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}

	header, err := types.DecodeDutiesHeader(meta)
	if err != nil {
		return nil, err
	}

	d := &types.EpochDuties{
		FirstSlot:         firstSlot,
		Epoch:             header.Epoch,
		ValidatorCount:    header.ValidatorCount,
		SlotsPerEpoch:     header.SlotsPerEpoch,
		CommitteesPerSlot: header.CommitteesPerSlot,
		PtcSize:           header.PtcSize,
		Committees:        make([][][]uint64, header.SlotsPerEpoch),
	}
	if header.PtcSize > 0 {
		d.Ptc = make([][]uint64, header.SlotsPerEpoch)
	}

	for slotIndex := range header.SlotsPerEpoch {
		blob, err := e.getDutiesValue(MakeDutiesKey(firstSlot, DutiesRecordCommittees, uint16(slotIndex)))
		if err != nil {
			return nil, err
		}
		if blob != nil {
			committees, err := types.DecodeSlotCommittees(blob)
			if err != nil {
				return nil, err
			}
			d.Committees[slotIndex] = committees
		}

		if header.PtcSize > 0 {
			ptcBlob, err := e.getDutiesValue(MakeDutiesKey(firstSlot, DutiesRecordPtc, uint16(slotIndex)))
			if err != nil {
				return nil, err
			}
			if ptcBlob != nil {
				d.Ptc[slotIndex] = types.DecodeIndexList(ptcBlob, types.DutiesIndexWidth)
			}
		}
	}

	return d, nil
}

// GetSlotCommittees reads the attester committees for a single slot.
func (e *PebbleEngine) GetSlotCommittees(_ context.Context, firstSlot uint64, slot uint64) ([][]uint64, error) {
	slotIndex := slot - firstSlot
	blob, err := e.getDutiesValue(MakeDutiesKey(firstSlot, DutiesRecordCommittees, uint16(slotIndex)))
	if err != nil || blob == nil {
		return nil, err
	}
	return types.DecodeSlotCommittees(blob)
}

// GetSlotPtc reads the PTC members for a single slot.
func (e *PebbleEngine) GetSlotPtc(_ context.Context, firstSlot uint64, slot uint64) ([]uint64, error) {
	slotIndex := slot - firstSlot
	blob, err := e.getDutiesValue(MakeDutiesKey(firstSlot, DutiesRecordPtc, uint16(slotIndex)))
	if err != nil || blob == nil {
		return nil, err
	}
	return types.DecodeIndexList(blob, types.DutiesIndexWidth), nil
}

// GetEpochDutiesForRoot reads and reassembles the diverging-fork duties for an
// epoch under the given dependent root. Returns nil, nil if not found.
func (e *PebbleEngine) GetEpochDutiesForRoot(_ context.Context, firstSlot uint64, depRoot [32]byte) (*types.EpochDuties, error) {
	meta, err := e.getDutiesValue(MakeDivergingDutiesKey(firstSlot, depRoot, DutiesRecordMeta, 0))
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}

	header, err := types.DecodeDutiesHeader(meta)
	if err != nil {
		return nil, err
	}

	d := &types.EpochDuties{
		FirstSlot:         firstSlot,
		Epoch:             header.Epoch,
		ValidatorCount:    header.ValidatorCount,
		SlotsPerEpoch:     header.SlotsPerEpoch,
		CommitteesPerSlot: header.CommitteesPerSlot,
		PtcSize:           header.PtcSize,
		DependentRoot:     header.DependentRoot,
		Committees:        make([][][]uint64, header.SlotsPerEpoch),
	}
	if header.PtcSize > 0 {
		d.Ptc = make([][]uint64, header.SlotsPerEpoch)
	}

	for slotIndex := range header.SlotsPerEpoch {
		blob, err := e.getDutiesValue(MakeDivergingDutiesKey(firstSlot, depRoot, DutiesRecordCommittees, uint16(slotIndex)))
		if err != nil {
			return nil, err
		}
		if blob != nil {
			committees, err := types.DecodeSlotCommittees(blob)
			if err != nil {
				return nil, err
			}
			d.Committees[slotIndex] = committees
		}

		if header.PtcSize > 0 {
			ptcBlob, err := e.getDutiesValue(MakeDivergingDutiesKey(firstSlot, depRoot, DutiesRecordPtc, uint16(slotIndex)))
			if err != nil {
				return nil, err
			}
			if ptcBlob != nil {
				d.Ptc[slotIndex] = types.DecodeIndexList(ptcBlob, types.DutiesIndexWidth)
			}
		}
	}

	return d, nil
}

// GetSlotCommitteesForRoot reads the attester committees for a single slot of a
// diverging fork identified by depRoot.
func (e *PebbleEngine) GetSlotCommitteesForRoot(_ context.Context, firstSlot uint64, slot uint64, depRoot [32]byte) ([][]uint64, error) {
	slotIndex := slot - firstSlot
	blob, err := e.getDutiesValue(MakeDivergingDutiesKey(firstSlot, depRoot, DutiesRecordCommittees, uint16(slotIndex)))
	if err != nil || blob == nil {
		return nil, err
	}
	return types.DecodeSlotCommittees(blob)
}

// GetSlotPtcForRoot reads the PTC members for a single slot of a diverging fork.
func (e *PebbleEngine) GetSlotPtcForRoot(_ context.Context, firstSlot uint64, slot uint64, depRoot [32]byte) ([]uint64, error) {
	slotIndex := slot - firstSlot
	blob, err := e.getDutiesValue(MakeDivergingDutiesKey(firstSlot, depRoot, DutiesRecordPtc, uint16(slotIndex)))
	if err != nil || blob == nil {
		return nil, err
	}
	return types.DecodeIndexList(blob, types.DutiesIndexWidth), nil
}

// HasEpochDuties checks if duties exist for an epoch (via its meta key).
func (e *PebbleEngine) HasEpochDuties(_ context.Context, firstSlot uint64) (bool, error) {
	_, closer, err := e.db.Get(MakeDutiesKey(firstSlot, DutiesRecordMeta, 0))
	if err == pebble.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_ = closer.Close()
	return true, nil
}

// PruneEpochDutiesBefore deletes all duties records for epochs whose first slot
// is before maxFirstSlot. Returns the number of epochs deleted. The DeleteRange
// spans the whole duties namespace up to maxFirstSlot, so diverging-fork keys
// (which share the namespace and sort by their firstSlot prefix) are removed
// together with the canonical keys; the epoch counter only tallies canonical
// meta keys (length-distinct at DutiesKeyLen).
func (e *PebbleEngine) PruneEpochDutiesBefore(_ context.Context, maxFirstSlot uint64) (int64, error) {
	rangeStart := makeNamespaceRangeStart(KeyNamespaceDuties)
	rangeEnd := makeNamespaceSlotKey(KeyNamespaceDuties, maxFirstSlot)

	var epochs int64

	iter, err := e.db.NewIter(&pebble.IterOptions{
		LowerBound: rangeStart,
		UpperBound: rangeEnd,
	})
	if err != nil {
		return 0, err
	}
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) == DutiesKeyLen && key[10] == DutiesRecordMeta {
			epochs++
		}
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}

	if epochs == 0 {
		return 0, nil
	}

	if err := e.db.DeleteRange(rangeStart, rangeEnd, pebble.Sync); err != nil {
		return 0, err
	}

	return epochs, nil
}
