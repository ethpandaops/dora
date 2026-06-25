package services

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/dbtypes"
)

// builderWithdrawalCredType is BUILDER_WITHDRAWAL_PREFIX (Gloas/EIP-8282): the 0x03 withdrawal
// credential prefix that marks a deposit as a builder deposit, onboarded as a builder at the Gloas
// fork transition (onboard_builders_from_pending_deposits).
const builderWithdrawalCredType uint8 = 0x03

// isBuilderCredential reports whether the withdrawal credentials use the builder prefix (0x03).
func isBuilderCredential(wc []byte) bool {
	return len(wc) > 0 && wc[0] == builderWithdrawalCredType
}

// projectionFetchCap bounds how many builder-credential deposits the projection enumerates. It is
// far above any realistic count of pre-fork builder deposits; hitting it sets Truncated.
const projectionFetchCap = 10000

// ProjectedBuilderDeposit is one builder-credential (0x03) deposit on chain together with its
// projected fate at the upcoming Gloas fork transition. When none of the fate flags is set the
// deposit is projected to be onboarded as a builder (Result distinguishes new vs top-up).
type ProjectedBuilderDeposit struct {
	Deposit *dbtypes.DepositWithTx

	// Result is the onboarding outcome (dbtypes.BuilderDepositRequestResult*): NewBuilder, TopUp, or
	// InvalidSignature. Left Unknown for deposits that never reach onboarding (too early / kept).
	Result uint8

	// EstimateEpoch is the projected epoch the deposit is processed (0 = unknown).
	EstimateEpoch phase0.Epoch

	// Fate flags (at most one set; none set => onboarded as a builder):
	TooEarly         bool // processed before the fork -> becomes a regular validator, not a builder
	AlreadyProcessed bool // refinement of TooEarly: already applied (vs projected to apply before fork)
	KeptAsValidator  bool // shares a pubkey with a validator -> applied as a validator deposit
	InvalidSignature bool // invalid proof-of-possession -> dropped at onboarding

	// Queue cross-check (the queue lags up to an epoch, so recent deposits may be unqueued).
	IsQueued bool
	QueuePos uint64
}

// Onboarded reports whether the deposit is projected to be onboarded as a builder at the fork.
func (d *ProjectedBuilderDeposit) Onboarded() bool {
	return !d.TooEarly && !d.KeptAsValidator && !d.InvalidSignature
}

// BuilderOnboardingProjection is the projected outcome, computed pre-Gloas, of the one-time builder
// onboarding at the Gloas fork transition. Its primary data source is the on-chain builder-credential
// deposits (so deposits made in the epoch before the fork — not yet reflected in the once-per-epoch
// queue snapshot — and already-applied deposits are both covered); the pending deposit queue is used
// only to cross-check queue position and the churn-based processing estimate. It is an estimate: a
// snapshot of current deposits and churn that does not model future deposits.
type BuilderOnboardingProjection struct {
	GloasForkEpoch phase0.Epoch
	GloasForkTime  time.Time

	// Deposits holds every (canonical) builder-credential deposit on chain, in deposit-index order,
	// each annotated with its projected fate.
	Deposits []*ProjectedBuilderDeposit

	OnboardedNewCount     uint64
	OnboardedTopUpCount   uint64
	TooEarlyCount         uint64
	InvalidSignatureCount uint64
	KeptAsValidatorCount  uint64

	// QueueEstimation is the epoch the current queue fully drains (0 = unknown / empty); and a
	// secondary stat: all queued deposits (any credentials) processed before the fork epoch.
	QueueEstimation               phase0.Epoch
	TotalQueueProcessedBeforeFork uint64

	// HasSafetyEstimate / DepositSafe answer "is it safe to deposit right now". DepositSafe is true
	// when a deposit submitted now would still be queued at the fork (and so onboarded as a builder);
	// when false the queue is too short and it would be processed as a validator before the fork.
	HasSafetyEstimate       bool
	DepositSafe             bool
	NewDepositEstimateEpoch phase0.Epoch
	NewDepositEstimateTime  time.Time

	// Truncated reports that the deposit enumeration hit projectionFetchCap (some deposits omitted).
	Truncated bool
}

// GetBuilderOnboardingProjection projects the builders that would be onboarded at the Gloas fork
// transition from the builder-credential deposits made before the fork. It returns nil when Gloas is
// not scheduled (no finite fork epoch) or the chain head/queue cannot be resolved. It is meant for
// the builder deposits page before the fork, where the real builder_deposits table is still empty.
//
// It enumerates every 0x03-credential deposit and classifies each: deposits already applied or that
// the churn queue would process before the fork become regular validators ("too early"); the rest
// register builders (or top up earlier ones), drop on an invalid proof-of-possession, or stay as
// validator deposits when they share a pubkey with a validator — mirroring
// onboard_builders_from_pending_deposits over the deposits that survive to the fork.
func (bs *ChainService) GetBuilderOnboardingProjection(ctx context.Context) *BuilderOnboardingProjection {
	chainState := bs.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if specs == nil || specs.GloasForkEpoch == nil || *specs.GloasForkEpoch == math.MaxUint64 {
		return nil
	}
	gloasForkEpoch := phase0.Epoch(*specs.GloasForkEpoch)

	canonicalHead := bs.beaconIndexer.GetCanonicalHead(nil)
	if canonicalHead == nil {
		return nil
	}
	indexedQueue := bs.GetIndexedDepositQueue(ctx, canonicalHead)
	if indexedQueue == nil {
		return nil
	}

	proj := &BuilderOnboardingProjection{
		GloasForkEpoch:  gloasForkEpoch,
		GloasForkTime:   chainState.EpochToTime(gloasForkEpoch),
		QueueEstimation: indexedQueue.QueueEstimation,
	}

	// remainsAtFork reports whether a queue entry survives process_pending_deposits up to the fork
	// epoch (EpochEstimate == 0 means unknown — postponed without estimate or no active balance —
	// and is treated as remaining, sitting at the back of the queue).
	remainsAtFork := func(epoch phase0.Epoch) bool {
		return epoch == 0 || epoch >= gloasForkEpoch
	}

	// Queue cross-check map + secondary stats. queueNonBuilderPubkeys collects pubkeys with a pending
	// non-builder deposit remaining at the fork, so a same-pubkey builder deposit is kept as a
	// validator deposit rather than onboarded.
	pendingByIndex := make(map[uint64]*IndexedDepositQueueEntry, len(indexedQueue.Queue))
	queueNonBuilderPubkeys := make(map[phase0.BLSPubKey]bool)
	for _, entry := range indexedQueue.Queue {
		if entry.DepositIndex != nil {
			pendingByIndex[*entry.DepositIndex] = entry
		}
		remains := remainsAtFork(entry.EpochEstimate)
		if !remains {
			proj.TotalQueueProcessedBeforeFork++
		}
		if remains && !isBuilderCredential(entry.PendingDeposit.WithdrawalCredentials) {
			queueNonBuilderPubkeys[entry.PendingDeposit.Pubkey] = true
		}
	}

	anchorIndex, hasAnchor := uint64(0), indexedQueue.LastIncludedDepositIndex != nil
	if hasAnchor {
		anchorIndex = *indexedQueue.LastIncludedDepositIndex
	}

	// tailEstimate is the projected processing epoch of a deposit appended to the tail right now —
	// used both for deposits made after the queue snapshot and for the deposit-safety indicator.
	depositAmount := phase0.Gwei(specs.MinActivationBalance)
	if depositAmount == 0 {
		depositAmount = phase0.Gwei(specs.MaxEffectiveBalance)
	}
	tailEstimate := indexedQueue.EstimateAppendedDepositEpoch(depositAmount)

	// Primary source: every builder-credential (0x03) deposit on chain (cache + DB merge). The
	// credential-type filter lives on the tx filter (both the cache and DB paths apply it there).
	depositFilter := &dbtypes.DepositFilter{
		WithOrphaned: 1,
	}
	txFilter := &dbtypes.DepositTxFilter{
		WithValid:           1, // no signature-validity filter — invalid sigs are a classified outcome
		WithdrawalCredTypes: []uint8{builderWithdrawalCredType},
	}
	deposits, _ := bs.GetDepositOperationsByFilter(ctx, depositFilter, txFilter, 0, projectionFetchCap)
	if len(deposits) >= projectionFetchCap {
		proj.Truncated = true
	}

	// Process in deposit-index order (= queue / processing order); unresolved-index deposits (very
	// recent) sort last.
	sort.SliceStable(deposits, func(i, j int) bool {
		return depositIndexOrMax(deposits[i]) < depositIndexOrMax(deposits[j])
	})

	isExistingValidator := func(pubkey phase0.BLSPubKey) bool {
		idx, found := bs.beaconIndexer.GetValidatorIndexByPubkey(pubkey)
		return found && !bs.IsProjectedValidatorIndex(idx)
	}

	acceptedBuilderPubkeys := make(map[phase0.BLSPubKey]bool) // pubkeys onboarded as new builders so far
	validatorFromDeposit := make(map[phase0.BLSPubKey]bool)   // pubkeys turned into validators by an earlier too-early deposit
	for _, dep := range deposits {
		if dep.Orphaned || !isBuilderCredential(dep.WithdrawalCredentials) {
			continue
		}

		var pubkey phase0.BLSPubKey
		copy(pubkey[:], dep.PublicKey)

		pd := &ProjectedBuilderDeposit{Deposit: dep}

		var queueEntry *IndexedDepositQueueEntry
		if dep.Index != nil {
			if qe, ok := pendingByIndex[*dep.Index]; ok {
				queueEntry = qe
				pd.IsQueued = true
				pd.QueuePos = qe.QueuePos
			}
		}

		// Determine the processing fate: still pending at the fork, already applied, or processed
		// before the fork by the churn queue.
		var remains bool
		switch {
		case queueEntry != nil:
			pd.EstimateEpoch = queueEntry.EpochEstimate
			remains = remainsAtFork(queueEntry.EpochEstimate)
		case dep.Index == nil || !hasAnchor || *dep.Index > anchorIndex:
			// included after the queue snapshot (recent) — still pending; estimate via the tail.
			pd.EstimateEpoch = tailEstimate
			remains = remainsAtFork(tailEstimate)
		default:
			// included by the snapshot but absent from the queue — already applied as a validator.
			pd.AlreadyProcessed = true
			remains = false
		}

		if !remains {
			pd.TooEarly = true
			proj.TooEarlyCount++
			validatorFromDeposit[pubkey] = true
			proj.Deposits = append(proj.Deposits, pd)
			continue
		}

		// Remains at the fork — onboarding selection (sequential, mirrors onboarding).
		validSig := true
		if dep.ValidSignature != nil {
			validSig = *dep.ValidSignature == 1 || *dep.ValidSignature == 2
		}

		switch {
		case acceptedBuilderPubkeys[pubkey]:
			pd.Result = dbtypes.BuilderDepositRequestResultTopUp
			proj.OnboardedTopUpCount++
		case isExistingValidator(pubkey) || validatorFromDeposit[pubkey] || queueNonBuilderPubkeys[pubkey]:
			pd.KeptAsValidator = true
			proj.KeptAsValidatorCount++
		case !validSig:
			pd.InvalidSignature = true
			pd.Result = dbtypes.BuilderDepositRequestResultInvalidSignature
			proj.InvalidSignatureCount++
		default:
			pd.Result = dbtypes.BuilderDepositRequestResultNewBuilder
			acceptedBuilderPubkeys[pubkey] = true
			proj.OnboardedNewCount++
		}

		proj.Deposits = append(proj.Deposits, pd)
	}

	if tailEstimate > 0 {
		proj.HasSafetyEstimate = true
		proj.NewDepositEstimateEpoch = tailEstimate
		proj.NewDepositEstimateTime = chainState.EpochToTime(tailEstimate)
		proj.DepositSafe = tailEstimate >= gloasForkEpoch
	}

	return proj
}

// depositIndexOrMax returns the deposit's EL index, or MaxUint64 when it is unresolved (so such
// deposits sort to the end).
func depositIndexOrMax(dep *dbtypes.DepositWithTx) uint64 {
	if dep.Index != nil {
		return *dep.Index
	}
	return math.MaxUint64
}
