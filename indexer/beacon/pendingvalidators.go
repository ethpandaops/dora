package beacon

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/ethpandaops/dora/indexer/beacon/depositsig"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
)

// pendingValidatorProjector derives the validators that the chain will create from
// the pending_deposits queue, with their exact future indices, so they can be shown
// in the validator set before the chain processes them.
//
// The projection is exact: process_pending_deposits appends new validators strictly
// in queue order, and the only queue reordering (postponed deposits) applies to
// validators that already exist on chain, so new-validator ordering is stable. A new
// pubkey becomes a validator at the first queue occurrence carrying a valid deposit
// signature — the same gate the state transition applies.
//
// Signature validity is cached by deposit identity. The queue only shrinks from the
// front and grows at the tail, so after warm-up each epoch only verifies the few
// newly arrived deposits; verification of those is batched (aggregated) into a single
// check in the common all-valid case.
type pendingValidatorProjector struct {
	indexer    *Indexer
	cacheMutex sync.RWMutex
	sigCache   map[[32]byte]bool // deposit identity -> signature validity
}

func newPendingValidatorProjector(indexer *Indexer) *pendingValidatorProjector {
	return &pendingValidatorProjector{
		indexer:  indexer,
		sigCache: make(map[[32]byte]bool),
	}
}

// project returns the validators the chain will create from the pending_deposits
// queue, in chain-assignment order starting at index len(realValidators), together
// with their starting balances. realValidators is the authoritative on-chain
// registry for the state being processed.
func (p *pendingValidatorProjector) project(realValidators []*phase0.Validator, deposits []*electra.PendingDeposit, in pendingProjectionInput) ([]*phase0.Validator, []phase0.Gwei) {
	if len(deposits) == 0 {
		return nil, nil
	}

	domain := depositsig.Domain(in.genesisForkVersion)

	// A Gloas fork that is scheduled but not yet active filters builder (0x03)
	// deposits: any still queued at the fork is onboarded as a builder
	// (onboard_builders_from_pending_deposits) and never becomes a validator, so it
	// only creates a validator if processed before the fork.
	gloasFilterActive := in.gloasForkEpoch != nil && in.currentEpoch < *in.gloasForkEpoch

	// Pubkeys already in the on-chain registry never create a validator (top-ups).
	// Sum the active balance in the same pass — it feeds the churn-based processing
	// epoch estimate below.
	known := make(map[phase0.BLSPubKey]struct{}, len(realValidators))
	totalActiveBalance := phase0.Gwei(0)
	for _, v := range realValidators {
		known[v.PublicKey] = struct{}{}
		if isActiveAtEpoch(v, in.currentEpoch) {
			totalActiveBalance += v.EffectiveBalance
		}
	}

	// Resolve signature validity for every creation candidate (deposit whose pubkey
	// is not yet on chain). Validity depends only on (pubkey, wc, amount, sig),
	// independent of queue order, so the whole set can be resolved as one batch.
	sigValid := p.resolveSignatures(deposits, known, domain)

	// Estimate the epoch at which each deposit will be processed. It is stored as the
	// projected validator's activation epoch (see validatorFromDeposit) — the work is
	// reused rather than discarded, and it also drives the Gloas builder filter.
	estimator := newDepositProcessingEstimator(in.currentEpoch, in.depositBalanceToConsume, phase0.Gwei(in.churnLimit(uint64(totalActiveBalance))), in.maxPendingDepositsPerEpoch)

	// Walk the queue in order, creating a validator at the first valid-signature
	// occurrence of each new pubkey.
	created := make(map[phase0.BLSPubKey]struct{})
	projectedValidators := make([]*phase0.Validator, 0)
	projectedBalances := make([]phase0.Gwei, 0)

	for i, deposit := range deposits {
		// The estimate must advance for every queued deposit (each consumes churn),
		// so step it before any skip.
		processingEpoch := estimator.next(deposit.Amount)

		if _, ok := known[deposit.Pubkey]; ok {
			continue // top-up to an existing on-chain validator
		}
		if _, ok := created[deposit.Pubkey]; ok {
			continue // top-up to an already-projected validator
		}
		if !sigValid[i] {
			continue // invalid signature: the chain drops it, pubkey stays uncreated
		}
		if gloasFilterActive && isBuilderWithdrawalCredential(deposit.WithdrawalCredentials) && processingEpoch >= *in.gloasForkEpoch {
			continue // onboarded as a builder at the Gloas fork; never becomes a validator
		}

		created[deposit.Pubkey] = struct{}{}
		projectedValidators = append(projectedValidators, validatorFromDeposit(deposit, processingEpoch))
		projectedBalances = append(projectedBalances, 0)
	}

	fmt.Println("projectedValidators", len(projectedValidators))

	return projectedValidators, projectedBalances
}

// pendingProjectionInput carries the chain context the projection needs. It is
// passed by value so the projector stays decoupled from ChainState and testable.
type pendingProjectionInput struct {
	genesisForkVersion         phase0.Version
	currentEpoch               phase0.Epoch
	depositBalanceToConsume    phase0.Gwei
	maxPendingDepositsPerEpoch uint64
	gloasForkEpoch             *phase0.Epoch                          // nil if no Gloas fork is scheduled
	churnLimit                 func(totalActiveBalance uint64) uint64 // activation/exit churn limit for a given active balance
}

// isActiveAtEpoch reports whether a validator is active at the given epoch.
func isActiveAtEpoch(v *phase0.Validator, epoch phase0.Epoch) bool {
	return v.ActivationEpoch <= epoch && epoch < v.ExitEpoch
}

// IsProjectedValidator reports whether a validator entry is a projection of a
// not-yet-processed pending deposit rather than a real on-chain validator. Such
// entries carry an unset activation-eligibility epoch together with a set
// activation epoch (the estimated processing epoch) — a combination a real
// validator never has, so its index is an estimate, not yet reserved on chain.
func IsProjectedValidator(v *phase0.Validator) bool {
	return v != nil && v.ActivationEligibilityEpoch == FarFutureEpoch && v.ActivationEpoch != FarFutureEpoch
}

// isBuilderWithdrawalCredential reports whether the credentials use the Gloas
// builder prefix (0x03).
func isBuilderWithdrawalCredential(wc []byte) bool {
	return len(wc) > 0 && wc[0] == 0x03
}

// depositProcessingEstimator estimates the epoch at which each pending deposit is
// processed, mirroring the churn/per-epoch-count stepping of process_pending_deposits
// (and the existing GetIndexedDepositQueue estimator). It is an approximation: it
// does not model exited/withdrawn deposits that skip churn, which is acceptable for
// the Gloas builder-deposit cutoff.
type depositProcessingEstimator struct {
	epoch                      phase0.Epoch
	balance                    phase0.Gwei
	churnLimit                 phase0.Gwei
	maxPendingDepositsPerEpoch uint64
	countInEpoch               uint64
}

func newDepositProcessingEstimator(currentEpoch phase0.Epoch, depositBalanceToConsume phase0.Gwei, churnLimit phase0.Gwei, maxPendingDepositsPerEpoch uint64) *depositProcessingEstimator {
	if maxPendingDepositsPerEpoch == 0 {
		maxPendingDepositsPerEpoch = 16
	}
	return &depositProcessingEstimator{
		epoch:                      currentEpoch + 1,
		balance:                    depositBalanceToConsume + churnLimit,
		churnLimit:                 churnLimit,
		maxPendingDepositsPerEpoch: maxPendingDepositsPerEpoch,
	}
}

// next returns the estimated processing epoch for a deposit of the given amount and
// advances the estimator. With no churn budget (degenerate empty chain) it returns
// FarFutureEpoch so dependent cutoffs treat the deposit as never processed.
func (e *depositProcessingEstimator) next(amount phase0.Gwei) phase0.Epoch {
	if e.churnLimit == 0 {
		return FarFutureEpoch
	}
	if e.countInEpoch >= e.maxPendingDepositsPerEpoch {
		e.epoch++
		e.balance = e.churnLimit
		e.countInEpoch = 0
	}
	for e.balance < amount {
		e.epoch++
		e.balance += e.churnLimit
		e.countInEpoch = 0
	}
	e.countInEpoch++
	e.balance -= amount
	return e.epoch
}

// resolveSignatures returns per-deposit signature validity (indexed like deposits),
// consulting the cache and batch-verifying any uncached creation candidates. Top-up
// deposits (pubkey already on chain) are skipped — their signature is irrelevant.
func (p *pendingValidatorProjector) resolveSignatures(deposits []*electra.PendingDeposit, known map[phase0.BLSPubKey]struct{}, domain zrnt_common.BLSDomain) []bool {
	sigValid := make([]bool, len(deposits))
	keys := make([][32]byte, len(deposits))
	isCandidate := make([]bool, len(deposits))
	candidateCount := 0

	toVerify := make([]depositsig.Input, 0)
	toVerifyIdx := make([]int, 0)

	p.cacheMutex.RLock()
	for i := range deposits {
		deposit := deposits[i]
		if _, ok := known[deposit.Pubkey]; ok {
			continue // top-up, signature irrelevant
		}
		isCandidate[i] = true
		candidateCount++
		keys[i] = depositIdentity(deposit)

		if valid, cached := p.sigCache[keys[i]]; cached {
			sigValid[i] = valid
			continue
		}
		toVerify = append(toVerify, depositsig.Input{
			Pubkey:                deposit.Pubkey,
			WithdrawalCredentials: deposit.WithdrawalCredentials,
			Amount:                deposit.Amount,
			Signature:             deposit.Signature,
		})
		toVerifyIdx = append(toVerifyIdx, i)
	}
	p.cacheMutex.RUnlock()

	if len(toVerify) == 0 {
		return sigValid
	}

	results := depositsig.VerifyBatch(toVerify, domain)

	p.cacheMutex.Lock()
	for j, idx := range toVerifyIdx {
		sigValid[idx] = results[j]
		p.sigCache[keys[idx]] = results[j]
	}
	// Prune entries for deposits that have left the queue. Growth only happens here
	// (when new deposits are verified), so pruning is co-located with growth and the
	// cache stays bounded to roughly the active candidate set.
	if len(p.sigCache) > 2*candidateCount && candidateCount > 0 {
		current := make(map[[32]byte]struct{}, candidateCount)
		for i := range deposits {
			if isCandidate[i] {
				current[keys[i]] = struct{}{}
			}
		}
		for k := range p.sigCache {
			if _, ok := current[k]; !ok {
				delete(p.sigCache, k)
			}
		}
	}
	p.cacheMutex.Unlock()

	return sigValid
}

// depositIdentity returns a stable key for a pending deposit's signed contents.
func depositIdentity(deposit *electra.PendingDeposit) [32]byte {
	h := sha256.New()
	h.Write(deposit.Pubkey[:])
	h.Write(deposit.WithdrawalCredentials)
	var amount [8]byte
	binary.LittleEndian.PutUint64(amount[:], uint64(deposit.Amount))
	h.Write(amount[:])
	h.Write(deposit.Signature[:])
	var key [32]byte
	h.Sum(key[:0])
	return key
}

// validatorFromDeposit builds the placeholder validator entry for a projected
// pending deposit. It carries the real future pubkey and withdrawal credentials
// (so the 0x00/0x01/0x02 type and withdrawal address are correct) and a zero
// effective balance. The estimated processing epoch is stored in ActivationEpoch,
// while ActivationEligibilityEpoch stays FarFuture — a combination that never
// occurs for a real validator (one cannot have an activation epoch without first
// being eligible), so it unambiguously marks the entry as projected while still
// surfacing the estimate. When the chain later creates the validator at this index,
// the real balance and epochs replace the placeholder via the normal cache update.
func validatorFromDeposit(deposit *electra.PendingDeposit, processingEpoch phase0.Epoch) *phase0.Validator {
	return &phase0.Validator{
		PublicKey:                  deposit.Pubkey,
		WithdrawalCredentials:      append([]byte(nil), deposit.WithdrawalCredentials...),
		EffectiveBalance:           0,
		Slashed:                    false,
		ActivationEligibilityEpoch: FarFutureEpoch,
		ActivationEpoch:            processingEpoch,
		ExitEpoch:                  FarFutureEpoch,
		WithdrawableEpoch:          FarFutureEpoch,
	}
}
