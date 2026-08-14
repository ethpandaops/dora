package system_contracts

import (
	"math"
	"sync"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution"
)

// forkActivationResolver resolves the first el block number processed under a fork's rules -
// the block whose payload executes the contract's first request dequeue system call. Contract
// queues that accept requests before their activation fork (e.g. the Gloas builder contracts)
// stay locked until that block, and since el block numbers are monotonic counters that shift
// with missed slots, the activation block can only be observed from indexed post-fork blocks -
// never predicted from the fork schedule.
type forkActivationResolver struct {
	indexerCtx *execution.IndexerCtx

	// forkEpochSpec extracts the activation fork epoch from the chain specs
	// (nil / MaxUint64 = fork not scheduled)
	forkEpochSpec func(specs *consensus.ChainSpec) *uint64

	mutex     sync.Mutex
	forkCache map[beacon.ForkKey]uint64
}

// newForkActivationResolver creates a new fork activation block resolver for the fork epoch
// extracted by the given spec callback.
func newForkActivationResolver(indexerCtx *execution.IndexerCtx, forkEpochSpec func(specs *consensus.ChainSpec) *uint64) *forkActivationResolver {
	return &forkActivationResolver{
		indexerCtx:    indexerCtx,
		forkEpochSpec: forkEpochSpec,
		forkCache:     make(map[beacon.ForkKey]uint64, 4),
	}
}

// resolveActivationBlock returns the first el block number where request dequeuing is active
// on the given fork (nil = finalized canonical view). The finalized view only reports the
// block once the fork boundary is finalized, so its result is stable and safe to persist;
// unfinalized forks may disagree on the block number until then.
func (r *forkActivationResolver) resolveActivationBlock(fork *execution.ForkWithClients) (uint64, bool) {
	chainState := r.indexerCtx.ChainState

	specs := chainState.GetSpecs()
	if specs == nil {
		return 0, false
	}

	forkEpochSpec := r.forkEpochSpec(specs)
	if forkEpochSpec == nil || *forkEpochSpec == math.MaxUint64 {
		return 0, false
	}

	forkEpoch := phase0.Epoch(*forkEpochSpec)
	if chainState.CurrentEpoch() < forkEpoch {
		return 0, false
	}

	forkSlot := chainState.EpochToSlot(forkEpoch)

	if fork != nil {
		return r.resolveUnfinalizedActivationBlock(forkSlot, fork)
	}

	// Finalized view: the db only contiguously covers epochs the finalization routine has
	// already processed (which lags the finalized checkpoint by up to an epoch) and - while a
	// backfill sync is running - the epochs the synchronizer has written. Only report an
	// activation block found strictly below that coverage: a not-yet-written region around the
	// fork boundary must yield "unknown" rather than a later (wrong) block number.
	beaconIndexer := r.indexerCtx.BeaconIndexer

	coveredEpoch, _ := beaconIndexer.GetBlockCacheState()
	if syncRunning, syncEpoch := beaconIndexer.GetSynchronizerState(); syncRunning && syncEpoch < coveredEpoch {
		coveredEpoch = syncEpoch
	}

	if coveredEpoch <= forkEpoch {
		return 0, false
	}

	slot, blockNumber, found := db.GetFirstCanonicalElBlockNumber(r.indexerCtx.Ctx, uint64(forkSlot))
	if !found || phase0.Slot(slot) >= chainState.EpochToSlot(coveredEpoch) {
		return 0, false
	}

	return blockNumber, true
}

// resolveUnfinalizedActivationBlock resolves the activation block along a specific unfinalized
// fork from the block cache: the first block at or after the fork slot on that fork (or one of
// its parents) that carries an execution payload.
func (r *forkActivationResolver) resolveUnfinalizedActivationBlock(forkSlot phase0.Slot, fork *execution.ForkWithClients) (uint64, bool) {
	r.mutex.Lock()
	cachedBlock, isCached := r.forkCache[fork.ForkId]
	r.mutex.Unlock()

	if isCached {
		return cachedBlock, true
	}

	beaconIndexer := r.indexerCtx.BeaconIndexer

	// the walk needs the boundary region in the block cache; if it is already pruned, the
	// finalized resolution (which sets the persisted activation block) must be used instead
	_, prunedEpoch := beaconIndexer.GetBlockCacheState()
	if forkSlot < r.indexerCtx.ChainState.EpochToSlot(prunedEpoch) {
		return 0, false
	}

	headBlock := beaconIndexer.GetCanonicalHead(&fork.ForkId)
	if headBlock == nil {
		return 0, false
	}

	parentForkIds := beaconIndexer.GetParentForkIds(fork.ForkId)
	forkIds := make(map[beacon.ForkKey]bool, len(parentForkIds)+1)
	forkIds[fork.ForkId] = true
	for _, parentForkId := range parentForkIds {
		forkIds[parentForkId] = true
	}

	for slot := forkSlot; slot <= headBlock.Slot; slot++ {
		for _, block := range beaconIndexer.GetBlocksBySlot(slot) {
			if !forkIds[block.GetForkId()] {
				continue
			}

			blockIndex := block.GetBlockIndex(r.indexerCtx.Ctx)
			if blockIndex == nil || blockIndex.ExecutionNumber == 0 {
				continue
			}

			r.cacheActivationBlock(fork.ForkId, blockIndex.ExecutionNumber)

			return blockIndex.ExecutionNumber, true
		}
	}

	return 0, false
}

// cacheActivationBlock stores a resolved per-fork activation block, evicting entries of forks
// that no longer have a head. Forks come and go for as long as the boundary is unfinalized (an
// unbounded window on non-finalizing chains), so the eviction keeps the cache bounded by the
// number of live forks.
func (r *forkActivationResolver) cacheActivationBlock(forkId beacon.ForkKey, blockNumber uint64) {
	forkHeads := r.indexerCtx.BeaconIndexer.GetForkHeads()
	aliveForkIds := make(map[beacon.ForkKey]bool, len(forkHeads))
	for _, forkHead := range forkHeads {
		aliveForkIds[forkHead.ForkId] = true
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	for cachedForkId := range r.forkCache {
		if !aliveForkIds[cachedForkId] {
			delete(r.forkCache, cachedForkId)
		}
	}

	r.forkCache[forkId] = blockNumber
}
