package beacon

import (
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/utils"
)

type attestationArrival struct {
	seenMs uint32
	root   phase0.Root
}

type attestationArrivalCache struct {
	indexer    *Indexer
	retention  phase0.Slot
	cacheMutex sync.RWMutex
	slots      map[phase0.Slot]map[phase0.ValidatorIndex]attestationArrival
}

func newAttestationArrivalCache(indexer *Indexer) *attestationArrivalCache {
	retention := utils.Config.Indexer.AttestationArrivals.RetentionSlots
	if retention == 0 {
		retention = 256
	}

	return &attestationArrivalCache{
		indexer:   indexer,
		retention: phase0.Slot(retention),
		slots:     make(map[phase0.Slot]map[phase0.ValidatorIndex]attestationArrival, retention),
	}
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

	arrivals := cache.slots[evt.Slot]
	if arrivals == nil {
		arrivals = make(map[phase0.ValidatorIndex]attestationArrival, 512)
		cache.slots[evt.Slot] = arrivals
	}

	if first, seen := arrivals[evt.AttesterIndex]; seen && first.seenMs <= seenMs {
		return
	}

	arrivals[evt.AttesterIndex] = attestationArrival{seenMs: seenMs, root: evt.BeaconBlockRoot}
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

func (cache *attestationArrivalCache) getArrivals(slot phase0.Slot, blockRoot *phase0.Root) (map[phase0.ValidatorIndex]uint32, uint64) {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	arrivals := cache.slots[slot]
	if arrivals == nil {
		return nil, 0
	}

	result := make(map[phase0.ValidatorIndex]uint32, len(arrivals))
	otherRoots := uint64(0)

	for index, arrival := range arrivals {
		if blockRoot != nil && arrival.root != *blockRoot {
			otherRoots++

			continue
		}

		result[index] = arrival.seenMs
	}

	return result, otherRoots
}
