package beacon

import (
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/utils"
)

const defaultAttestationArrivalRetention = 256

type attestationArrival struct {
	seenMs uint32
	root   uint8
}

type slotAttestationArrivals struct {
	roots    []phase0.Root
	arrivals map[phase0.ValidatorIndex]attestationArrival
}

type attestationArrivalCache struct {
	indexer    *Indexer
	retention  phase0.Slot
	cacheMutex sync.RWMutex
	slots      map[phase0.Slot]*slotAttestationArrivals
}

func newAttestationArrivalCache(indexer *Indexer) *attestationArrivalCache {
	retention := utils.Config.Indexer.AttestationArrivals.RetentionSlots
	if retention == 0 {
		retention = defaultAttestationArrivalRetention
	}

	return &attestationArrivalCache{
		indexer:   indexer,
		retention: phase0.Slot(retention),
		slots:     make(map[phase0.Slot]*slotAttestationArrivals, retention),
	}
}

func (slot *slotAttestationArrivals) rootIndex(root phase0.Root) (uint8, bool) {
	for idx, known := range slot.roots {
		if known == root {
			return uint8(idx), true //nolint:gosec // bounded below
		}
	}

	if len(slot.roots) >= 255 {
		return 0, false
	}

	slot.roots = append(slot.roots, root)

	return uint8(len(slot.roots) - 1), true //nolint:gosec // bounded above
}

func (cache *attestationArrivalCache) addAttestation(evt *rpc.SingleAttestationEvent) {
	chainState := cache.indexer.consensusPool.GetChainState()

	currentSlot := chainState.CurrentSlot()
	if evt.Slot > currentSlot+1 || evt.Slot+cache.retention <= currentSlot {
		return
	}

	offset := evt.Seen.Sub(chainState.SlotToTime(evt.Slot)).Milliseconds()
	if offset < 0 {
		offset = 0
	}

	seenMs := uint32(offset) //nolint:gosec // bounded by the retention window

	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	slot := cache.slots[evt.Slot]
	if slot == nil {
		slot = &slotAttestationArrivals{arrivals: make(map[phase0.ValidatorIndex]attestationArrival, 512)}
		cache.slots[evt.Slot] = slot
	}

	if first, seen := slot.arrivals[evt.AttesterIndex]; seen && first.seenMs <= seenMs {
		return
	}

	root, ok := slot.rootIndex(evt.BeaconBlockRoot)
	if !ok {
		return
	}

	slot.arrivals[evt.AttesterIndex] = attestationArrival{seenMs: seenMs, root: root}
}

func (cache *attestationArrivalCache) processSlot(currentSlot phase0.Slot) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	for slot := range cache.slots {
		if slot+cache.retention <= currentSlot {
			delete(cache.slots, slot)
		}
	}
}

func (cache *attestationArrivalCache) getArrivals(slotNumber phase0.Slot, blockRoot *phase0.Root) (map[phase0.ValidatorIndex]uint32, uint64) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	slot := cache.slots[slotNumber]
	if slot == nil {
		return nil, 0
	}

	result := make(map[phase0.ValidatorIndex]uint32, len(slot.arrivals))
	otherRoots := uint64(0)

	for index, arrival := range slot.arrivals {
		if blockRoot != nil && slot.roots[arrival.root] != *blockRoot {
			otherRoots++

			continue
		}

		result[index] = arrival.seenMs
	}

	return result, otherRoots
}
