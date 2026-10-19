// Package inclusionlists resolves why execution payloads omitted transactions
// of the inclusion lists (EIP-7805) that constrain them.
//
// The inclusion lists published in a slot constrain the execution payload of
// the following slot. As soon as that payload is known, the resolver checks
// every list transaction against it once, reading the sender states at the
// payload's post-state while every node still has them, and hands the outcome
// to the beacon indexer, which persists it with the lists.
package inclusionlists

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/indexer/beacon"
	execindexer "github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/utils"
)

const (
	// resolveInterval is how often pending evaluations are looked for. Block
	// events trigger a pass as well; the interval covers payloads that arrive
	// after their block.
	resolveInterval = 2 * time.Second

	// maxStateAttempts is how often the sender state lookup of one target
	// block is tried before its transactions are left unresolved.
	maxStateAttempts = 5

	// stateRetryDelay is the pause between sender state lookup attempts.
	stateRetryDelay = 6 * time.Second

	// maxStateClients is the number of clients asked per attempt.
	maxStateClients = 6
)

// targetKey identifies the evaluation of a slot's lists against one block.
type targetKey struct {
	slot phase0.Slot
	root phase0.Root
}

// targetAttempts tracks the sender state lookups of one target block.
type targetAttempts struct {
	count   int
	nextTry time.Time
	// listCount is the number of lists the attempts were made for.
	listCount int
}

// blocked reports whether the target must not be evaluated now: its attempts
// are used up or the next one is not due yet. A list that was first seen
// after the attempts makes the evaluation incomplete in a way a new attempt
// can fix, so it lifts the block.
func (attempts *targetAttempts) blocked(listCount int, now time.Time) bool {
	if listCount > attempts.listCount {
		return false
	}

	return attempts.count >= maxStateAttempts || now.Before(attempts.nextTry)
}

// Resolver evaluates the inclusion lists of recent slots against the execution
// payloads they constrain.
type Resolver struct {
	indexerCtx *execindexer.IndexerCtx
	logger     logrus.FieldLogger

	// attempts tracks the targets whose sender state could not be loaded yet.
	// Only accessed by the resolver loop.
	attempts map[targetKey]*targetAttempts

	// unreliable holds the clients that answered a state request with the
	// state of another block. They are asked last. Only accessed by the
	// resolver loop.
	unreliable map[*execution.Client]bool

	multicallAddr   common.Address
	multicallReady  bool
	multicallProbed time.Time
}

// NewResolver creates a new inclusion list resolver.
func NewResolver(logger logrus.FieldLogger, indexerCtx *execindexer.IndexerCtx) *Resolver {
	return &Resolver{
		indexerCtx:    indexerCtx,
		logger:        logger,
		attempts:      make(map[targetKey]*targetAttempts, 16),
		unreliable:    make(map[*execution.Client]bool, 4),
		multicallAddr: multicallAddress(),
	}
}

// Start begins resolving. The resolver stops with the indexer context.
func (r *Resolver) Start() error {
	go r.runResolverLoop()

	return nil
}

// runResolverLoop evaluates pending targets on every block event and tick.
func (r *Resolver) runResolverLoop() {
	defer utils.HandleSubroutinePanic("inclusionlists.Resolver.runResolverLoop", r.runResolverLoop)

	subscription := r.indexerCtx.BeaconIndexer.SubscribeBlockEvent(100, false)
	defer subscription.Unsubscribe()

	ticker := time.NewTicker(resolveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.indexerCtx.Ctx.Done():
			return
		case <-subscription.Channel():
		case <-ticker.C:
		}

		r.resolvePending(r.indexerCtx.Ctx)
	}
}

// resolvePending evaluates the cached inclusion lists against all blocks of
// their following slot whose payload is known and not evaluated yet.
func (r *Resolver) resolvePending(ctx context.Context) {
	chainState := r.indexerCtx.ChainState
	if specs := chainState.GetSpecs(); specs == nil || !chainState.IsEip7805Enabled(chainState.CurrentEpoch()) {
		return
	}

	beaconIndexer := r.indexerCtx.BeaconIndexer
	slots := beaconIndexer.GetInclusionListSlots()

	cached := make(map[phase0.Slot]bool, len(slots))
	for _, slot := range slots {
		cached[slot] = true
		listCount := len(beaconIndexer.GetInclusionListsBySlot(slot))

		for _, block := range beaconIndexer.GetBlocksBySlot(slot + 1) {
			if !block.HasExecutionPayload() {
				continue
			}

			key := targetKey{slot: slot, root: block.Root}
			attempts := r.attempts[key]
			if attempts != nil {
				if attempts.blocked(listCount, time.Now()) {
					continue
				}
				if listCount > attempts.listCount {
					// A new list gives the target a fresh set of attempts.
					delete(r.attempts, key)
					attempts = nil
				}
			} else if eval := beaconIndexer.GetInclusionListEvaluation(slot, block.Root); eval != nil && len(eval.ListFlags) >= listCount {
				// A list that was first seen after the evaluation makes it
				// incomplete; it is redone to cover that list too.
				continue
			}

			complete, err := r.resolveTarget(ctx, slot, block)
			if err != nil {
				r.logger.WithError(err).Debugf("inclusion list evaluation for slot %d against block %d [0x%x] incomplete", slot, block.Slot, block.Root[:])
			}

			if complete {
				delete(r.attempts, key)
				continue
			}

			if attempts == nil {
				attempts = &targetAttempts{listCount: listCount}
				r.attempts[key] = attempts
			}
			attempts.count++
			attempts.nextTry = time.Now().Add(stateRetryDelay)
		}
	}

	for key := range r.attempts {
		if !cached[key.slot] {
			delete(r.attempts, key)
		}
	}
}

// resolveTarget evaluates the inclusion lists of the given slot against the
// execution payload of the given block of the following slot and stores the
// outcome. It returns false if the sender state could not be loaded, in which
// case the affected transactions are stored as unknown.
func (r *Resolver) resolveTarget(ctx context.Context, slot phase0.Slot, block *beacon.Block) (bool, error) {
	beaconIndexer := r.indexerCtx.BeaconIndexer
	chainState := r.indexerCtx.ChainState

	lists := beaconIndexer.GetInclusionListsBySlot(slot)
	if len(lists) == 0 {
		return true, nil
	}

	payload := executionPayload(ctx, block)
	if payload == nil {
		return false, fmt.Errorf("execution payload not available")
	}

	input := &evaluationInput{
		lists:              lists,
		blockRoot:          block.Root,
		payload:            payload,
		parentTransactions: r.parentTransactions(ctx, payload),
	}

	specs := chainState.GetSpecs()
	if specs.InclusionListDueBPS > 0 {
		input.dueMs = int32(uint64(chainState.GetSlotDuration(slot).Milliseconds()) * specs.InclusionListDueBPS / 10000)
	}
	input.dependentRoot, input.hasDependentRoot = beaconIndexer.GetShufflingDependentRoot(block, chainState.EpochOfSlot(slot))

	eval := newEvaluation(input)

	var stateErr error
	if senders := eval.senders(); len(senders) > 0 {
		var states map[common.Address]*senderState
		states, stateErr = r.loadSenderStates(ctx, common.Hash(payload.BlockHash), senders, payloadStateProbes(payload))
		if states != nil {
			eval.applySenderStates(states)
		}
	}

	beaconIndexer.SetInclusionListEvaluation(slot, eval.fragment)

	unsatisfied, complete := eval.result.CountUnsatisfied()
	if unsatisfied > 0 {
		r.logger.Infof("execution payload of slot %d [0x%x] leaves %d inclusion list transactions unsatisfied", block.Slot, block.Root[:], unsatisfied)
	}

	return complete, stateErr
}

// executionPayload returns the execution payload of a block, or nil.
func executionPayload(ctx context.Context, block *beacon.Block) *all.ExecutionPayload {
	envelope := block.GetExecutionPayload(ctx)
	if envelope == nil || envelope.Message == nil {
		return nil
	}

	return envelope.Message.Payload
}

// parentTransactions returns the raw transactions of the payload the given
// payload builds on. An inclusion list is built against the head its author
// saw, so it can carry transactions that this parent payload already included.
func (r *Resolver) parentTransactions(ctx context.Context, payload *all.ExecutionPayload) map[string]bool {
	for _, parent := range r.indexerCtx.BeaconIndexer.GetBlocksByExecutionBlockHash(payload.ParentHash) {
		parentPayload := executionPayload(ctx, parent)
		if parentPayload == nil {
			continue
		}

		transactions := make(map[string]bool, len(parentPayload.Transactions))
		for _, rawTx := range parentPayload.Transactions {
			transactions[string(rawTx)] = true
		}

		return transactions
	}

	return nil
}

// loadSenderStates loads the sender states at the given execution block,
// trying the ready clients in priority order. A client that does not answer
// at that block is skipped; one that failed a probe is remembered and asked
// last from then on.
func (r *Resolver) loadSenderStates(ctx context.Context, blockHash common.Hash, senders []common.Address, probes []*stateProbe) (map[common.Address]*senderState, error) {
	clients := r.indexerCtx.ExecutionPool.GetReadyEndpoints(execution.AnyClient)
	if len(clients) == 0 {
		return nil, fmt.Errorf("no ready execution clients")
	}

	sort.Slice(clients, func(i, j int) bool {
		return r.indexerCtx.SortClients(clients[i], clients[j], false)
	})
	sort.SliceStable(clients, func(i, j int) bool {
		return !r.unreliable[clients[i]] && r.unreliable[clients[j]]
	})

	if len(clients) > maxStateClients {
		clients = clients[:maxStateClients]
	}

	var lastErr error
	for _, client := range clients {
		ethClient := client.GetRPCClient().GetEthClient()
		if ethClient == nil {
			continue
		}

		states, err := r.fetchSenderStates(ctx, ethClient, blockHash, senders, probes)
		if err != nil {
			if len(probes) > 0 && errors.Is(err, errStateNotAtBlock) {
				r.unreliable[client] = true
			}
			lastErr = fmt.Errorf("sender states from %s: %w", client.GetName(), err)
			continue
		}

		return states, nil
	}

	return nil, lastErr
}
