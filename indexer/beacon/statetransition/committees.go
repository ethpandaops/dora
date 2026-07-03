package statetransition

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	blsu "github.com/protolambda/bls12-381-util"
)

// committeeKey uniquely identifies a beacon committee by slot and index.
type committeeKey struct {
	slot  phase0.Slot
	index uint64
}

// committeeCache caches computed beacon committees
// to avoid recomputation across multiple attestations.
//
// It also memoizes the full swap-or-not permutation for the current epoch's
// attester seed. Every committee in an epoch is a contiguous slice of the same
// shuffled active-index list, so the permutation is computed once and reused for
// all (slot, index) committees instead of being recomputed per committee.
type committeeCache struct {
	cache    map[committeeKey][]phase0.ValidatorIndex
	shuffled map[phase0.Root][]uint64
}

func newCommitteeCache() *committeeCache {
	return &committeeCache{
		cache:    make(map[committeeKey][]phase0.ValidatorIndex, 64),
		shuffled: make(map[phase0.Root][]uint64, 2),
	}
}

// shuffledIndices returns the memoized full permutation for the given seed,
// computing it on first use. result[i] equals compute_shuffled_index(i, n, seed);
// a committee for output positions [start, end) is therefore
// activeIndices[result[start:end]]. Keyed by seed so the current- and
// previous-epoch permutations (both referenced while processing a block's
// attestations) coexist without thrashing.
func (c *committeeCache) shuffledIndices(seed phase0.Root, n uint64, specs *consensus.ChainSpec) []uint64 {
	if list, ok := c.shuffled[seed]; ok && uint64(len(list)) == n {
		return list
	}

	list := computeShuffledList(n, seed, specs)
	c.shuffled[seed] = list

	return list
}

// get returns the cached committee, or nil if not cached.
func (c *committeeCache) get(slot phase0.Slot, index uint64) []phase0.ValidatorIndex {
	return c.cache[committeeKey{slot: slot, index: index}]
}

// put stores a committee in the cache.
func (c *committeeCache) put(slot phase0.Slot, index uint64, committee []phase0.ValidatorIndex) {
	c.cache[committeeKey{slot: slot, index: index}] = committee
}

// getCommitteeCountPerSlot returns the number of committees per slot for the given epoch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_committee_count_per_slot
func (s *stateAccessor) getCommitteeCountPerSlot(epoch phase0.Epoch) uint64 {
	activeCount := uint64(len(s.getActiveValidatorIndices(epoch)))
	committeesPerSlot := activeCount / s.specs.SlotsPerEpoch / s.specs.TargetCommitteeSize
	if committeesPerSlot > s.specs.MaxCommitteesPerSlot {
		committeesPerSlot = s.specs.MaxCommitteesPerSlot
	}
	if committeesPerSlot < 1 {
		committeesPerSlot = 1
	}
	return committeesPerSlot
}

// getBeaconCommittee returns the beacon committee for the given slot and committee index.
// Uses the provided cache to avoid recomputation.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#get_beacon_committee
func (s *stateAccessor) getBeaconCommittee(slot phase0.Slot, committeeIndex uint64, cc *committeeCache) []phase0.ValidatorIndex {
	if cc != nil {
		if cached := cc.get(slot, committeeIndex); cached != nil {
			return cached
		}
	}

	epoch := phase0.Epoch(uint64(slot) / s.specs.SlotsPerEpoch)
	committeesPerSlot := s.getCommitteeCountPerSlot(epoch)
	activeIndices := s.getActiveValidatorIndices(epoch)
	seed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainBeaconAttester))

	slotIndex := uint64(slot) % s.specs.SlotsPerEpoch
	index := slotIndex*committeesPerSlot + committeeIndex
	count := committeesPerSlot * s.specs.SlotsPerEpoch

	committee := computeCommittee(activeIndices, seed, index, count, s.specs, cc)

	if cc != nil {
		cc.put(slot, committeeIndex, committee)
	}

	return committee
}

// computeCommittee computes a committee from the given parameters.
// The committee is a contiguous slice [start, end) of the epoch's shuffled
// active-index list; cc memoizes that list so it is built once per epoch.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#compute_committee
func computeCommittee(indices []phase0.ValidatorIndex, seed phase0.Root, index, count uint64, specs *consensus.ChainSpec, cc *committeeCache) []phase0.ValidatorIndex {
	if count == 0 {
		return nil
	}

	indexCount := uint64(len(indices))
	start := (indexCount * index) / count
	end := (indexCount * (index + 1)) / count

	var shuffled []uint64
	if cc != nil {
		shuffled = cc.shuffledIndices(seed, indexCount, specs)
	} else {
		shuffled = computeShuffledList(indexCount, seed, specs)
	}

	committee := make([]phase0.ValidatorIndex, 0, end-start)
	for i := start; i < end; i++ {
		committee = append(committee, indices[shuffled[i]])
	}

	return committee
}

// computeShuffledList computes the full swap-or-not permutation for indexCount
// indices under the given seed: result[i] == compute_shuffled_index(i, indexCount, seed).
//
// It runs ShuffleRoundCount passes over the whole list, computing each round's
// pivot once and all of the round's source hashes into a flat slice (one hash
// per 256-position bucket). This replaces the per-committee, map-backed batch
// shuffle: a single pass serves every committee in the epoch, and the bucket
// lookup is an array index rather than a hashmap probe.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#compute_shuffled_index
func computeShuffledList(indexCount uint64, seed phase0.Root, specs *consensus.ChainSpec) []uint64 {
	result := make([]uint64, indexCount)
	for i := uint64(0); i < indexCount; i++ {
		result[i] = i
	}
	if indexCount < 2 {
		return result
	}

	bucketCount := (indexCount + 255) / 256
	sources := make([]phase0.Root, bucketCount)

	// Fan the per-round position updates out across the schedulable cores; they
	// are independent and dominate the cost. GOMAXPROCS (not NumCPU) so
	// container CPU quotas and user overrides are respected.
	workers := runtime.GOMAXPROCS(0)

	for currentRound := uint64(0); currentRound < specs.ShuffleRoundCount; currentRound++ {
		// Compute pivot once per round (depends only on seed + round).
		var buf [33]byte
		copy(buf[0:32], seed[:])
		buf[32] = byte(currentRound)
		pivotHash := hash256(buf[:])
		pivot := binary.LittleEndian.Uint64(pivotHash[:8]) % indexCount

		// Pre-compute every source hash for this round into a flat slice.
		var srcBuf [37]byte
		copy(srcBuf[0:32], seed[:])
		srcBuf[32] = byte(currentRound)
		for b := uint64(0); b < bucketCount; b++ {
			binary.LittleEndian.PutUint32(srcBuf[33:37], uint32(b))
			sources[b] = hash256(srcBuf[:])
		}

		// Apply this round's swap-or-not flips. Each output position is updated
		// independently from its own current value, so the loop parallelizes
		// cleanly; the next round depends on this round's full result, so the
		// rounds themselves stay sequential.
		shuffleRound(result, sources, pivot, indexCount, workers)
	}

	return result
}

// shuffleRound applies one swap-or-not round to result in place, dividing the
// index range across workers goroutines (or running inline for small inputs).
//
// Spawning fresh goroutines per round beats a persistent channel-fed worker
// pool here: the serial source-hash phase between rounds parks pool workers,
// and waking them via channel sends measures slower than fresh spawns.
func shuffleRound(result []uint64, sources []phase0.Root, pivot, indexCount uint64, workers int) {
	apply := func(start, end uint64) {
		for i := start; i < end; i++ {
			index := result[i]
			flip := (pivot + indexCount - index) % indexCount
			position := index
			if flip > index {
				position = flip
			}
			source := sources[position/256]
			byteIdx := (position % 256) / 8
			bitIdx := position % 8
			if (source[byteIdx]>>bitIdx)&1 == 1 {
				result[i] = flip
			}
		}
	}

	n := uint64(len(result))
	if workers <= 1 || n < 4096 {
		apply(0, n)
		return
	}

	var wg sync.WaitGroup
	chunk := (n + uint64(workers) - 1) / uint64(workers)
	for start := uint64(0); start < n; start += chunk {
		end := min(start+chunk, n)
		wg.Go(func() { apply(start, end) })
	}
	wg.Wait()
}

// getAttestingIndices returns the set of attesting indices for an Electra+ attestation.
// https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-get_attesting_indices
func (s *stateAccessor) getAttestingIndices(slot phase0.Slot, committeeBits []byte, aggregationBits []byte, cc *committeeCache) []phase0.ValidatorIndex {
	committeeIndices := getCommitteeIndicesFromBits(committeeBits)

	attestingSet := make(map[phase0.ValidatorIndex]struct{})
	committeeOffset := 0

	for _, ci := range committeeIndices {
		committee := s.getBeaconCommittee(slot, ci, cc)
		for i, validatorIndex := range committee {
			bitPos := committeeOffset + i
			byteIdx := bitPos / 8
			bitIdx := bitPos % 8
			if byteIdx < len(aggregationBits) && aggregationBits[byteIdx]&(1<<bitIdx) != 0 {
				attestingSet[validatorIndex] = struct{}{}
			}
		}
		committeeOffset += len(committee)
	}

	result := make([]phase0.ValidatorIndex, 0, len(attestingSet))
	for idx := range attestingSet {
		result = append(result, idx)
	}

	return result
}

// getCommitteeIndicesFromBits extracts the committee indices from a CommitteeBits bitvector.
func getCommitteeIndicesFromBits(committeeBits []byte) []uint64 {
	var indices []uint64
	for byteIdx, b := range committeeBits {
		for bitIdx := range 8 {
			if b&(1<<bitIdx) != 0 {
				indices = append(indices, uint64(byteIdx*8+bitIdx))
			}
		}
	}
	return indices
}

// processSyncCommitteeUpdates rotates the sync committee at period boundaries.
// New in Altair: https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#sync-committee-updates
func processSyncCommitteeUpdates(s *stateAccessor) {
	nextEpoch := s.currentEpoch() + 1
	if s.specs.EpochsPerSyncCommitteePeriod == 0 {
		return
	}
	if uint64(nextEpoch)%s.specs.EpochsPerSyncCommitteePeriod != 0 {
		return
	}

	s.CurrentSyncCommittee = s.NextSyncCommittee
	s.NextSyncCommittee = computeNextSyncCommittee(s)
}

// computeNextSyncCommittee computes the sync committee for the upcoming sync
// committee period by sampling validators weighted by effective balance.
//
// https://github.com/ethereum/consensus-specs/blob/master/specs/altair/beacon-chain.md#get_next_sync_committee
// Modified in Electra (16-bit random values): https://github.com/ethereum/consensus-specs/blob/master/specs/electra/beacon-chain.md#modified-get_next_sync_committee_indices
func computeNextSyncCommittee(s *stateAccessor) *altair.SyncCommittee {
	indices := s.getActiveValidatorIndices(s.currentEpoch() + 1)
	if len(indices) == 0 {
		return s.NextSyncCommittee // fallback: keep current
	}

	epoch := s.currentEpoch() + 1
	seed := getSeed(s, epoch, phase0.DomainType(s.specs.DomainSyncCommittee))

	syncCommitteeSize := s.specs.SyncCommitteeSize
	pubkeys := make([]phase0.BLSPubKey, 0, syncCommitteeSize)

	// Electra: 16-bit random values (MAX_RANDOM_VALUE = 2^16 - 1)
	const maxRandomValue = 65535
	maxEB := uint64(s.specs.MaxEffectiveBalanceElectra)
	if maxEB == 0 {
		maxEB = uint64(s.specs.MaxEffectiveBalance)
	}

	i := uint64(0)
	for uint64(len(pubkeys)) < syncCommitteeSize {
		shuffledIndex := computeShuffledIndex(i%uint64(len(indices)), uint64(len(indices)), seed, s.specs)
		candidateIndex := indices[shuffledIndex]

		// Electra: random_bytes = hash(seed + uint_to_bytes(i // 16))
		var rbuf [40]byte
		copy(rbuf[:32], seed[:])
		binary.LittleEndian.PutUint64(rbuf[32:40], i/16)
		h := hash256(rbuf[:])
		offset := (i % 16) * 2
		randomValue := uint64(h[offset]) | uint64(h[offset+1])<<8

		effectiveBalance := uint64(s.Validators[candidateIndex].EffectiveBalance)
		if effectiveBalance*maxRandomValue >= maxEB*randomValue {
			pubkeys = append(pubkeys, s.Validators[candidateIndex].PublicKey)
		}
		i++
	}

	aggregate, err := aggregateBLSPubkeys(pubkeys)
	if err != nil {
		// An aggregation failure means a malformed pubkey ended up in the
		// committee, which is impossible for a valid chain — surface it loudly
		// rather than silently producing a wrong state root.
		panic(fmt.Errorf("failed to aggregate sync committee pubkeys: %w", err))
	}

	return &altair.SyncCommittee{
		Pubkeys:         pubkeys,
		AggregatePubkey: aggregate,
	}
}

// aggregateBLSPubkeys computes the BLS G1 aggregate of the given pubkeys.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#bls-signatures
func aggregateBLSPubkeys(pubkeys []phase0.BLSPubKey) (phase0.BLSPubKey, error) {
	if len(pubkeys) == 0 {
		return phase0.BLSPubKey{}, fmt.Errorf("cannot aggregate empty pubkey set")
	}
	parsed := make([]*blsu.Pubkey, len(pubkeys))
	for i, pk := range pubkeys {
		var raw [48]byte = pk
		p := new(blsu.Pubkey)
		if err := p.Deserialize(&raw); err != nil {
			return phase0.BLSPubKey{}, fmt.Errorf("invalid pubkey at index %d: %w", i, err)
		}
		parsed[i] = p
	}
	agg, err := blsu.AggregatePubkeys(parsed)
	if err != nil {
		return phase0.BLSPubKey{}, err
	}
	out := agg.Serialize()
	return phase0.BLSPubKey(out), nil
}
