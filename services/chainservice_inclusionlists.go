package services

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

// InclusionListTxStatus classifies an inclusion list transaction against the payload
// that had to satisfy it, following the EIP-7805 inclusion list satisfaction check.
type InclusionListTxStatus uint8

const (
	ILTxStatusUnknown           InclusionListTxStatus = iota // could not be evaluated (state unavailable)
	ILTxStatusPending                                        // the constrained payload is not known yet
	ILTxStatusNotEnforced                                    // no block at slot+1, the list constrains nothing
	ILTxStatusIncluded                                       // present in the constrained payload
	ILTxStatusBlockFull                                      // tx gas limit exceeds the payload's remaining gas
	ILTxStatusNonceTooLow                                    // sender nonce already consumed
	ILTxStatusNonceTooHigh                                   // nonce gap, tx not executable yet
	ILTxStatusInsufficientFunds                              // sender cannot cover gas * fee cap + value
	ILTxStatusFeeCapTooLow                                   // fee cap below the payload's base fee
	ILTxStatusExpired                                        // expiry deadline passed
	ILTxStatusInvalid                                        // undecodable or sender not recoverable
	ILTxStatusUnsatisfied                                    // valid and fits, but left out: IL violation
	ILTxStatusUntimely                                       // only in lists seen after the due time, not enforced
	ILTxStatusEquivocation                                   // only in lists of equivocating members, not enforced
)

var inclusionListTxStatusLabels = map[InclusionListTxStatus][2]string{
	ILTxStatusUnknown:           {"Unknown", "text-bg-secondary"},
	ILTxStatusPending:           {"Pending", "text-bg-secondary"},
	ILTxStatusNotEnforced:       {"Not enforced", "text-bg-secondary"},
	ILTxStatusIncluded:          {"Included", "text-bg-success"},
	ILTxStatusBlockFull:         {"Block full", "text-bg-info"},
	ILTxStatusNonceTooLow:       {"Nonce too low", "text-bg-info"},
	ILTxStatusNonceTooHigh:      {"Nonce gap", "text-bg-info"},
	ILTxStatusInsufficientFunds: {"Insufficient funds", "text-bg-info"},
	ILTxStatusFeeCapTooLow:      {"Fee too low", "text-bg-info"},
	ILTxStatusExpired:           {"Expired", "text-bg-info"},
	ILTxStatusInvalid:           {"Invalid", "text-bg-info"},
	ILTxStatusUnsatisfied:       {"Unsatisfied", "text-bg-danger"},
	ILTxStatusUntimely:          {"Late IL", "text-bg-warning"},
	ILTxStatusEquivocation:      {"Equivocation", "text-bg-warning"},
}

// Label returns a short display name for the status.
func (s InclusionListTxStatus) Label() string {
	return inclusionListTxStatusLabels[s][0]
}

// BadgeClass returns the bootstrap badge class used to render the status.
func (s InclusionListTxStatus) BadgeClass() string {
	return inclusionListTxStatusLabels[s][1]
}

// InclusionListTxEvaluation is the outcome for a single inclusion list transaction.
type InclusionListTxEvaluation struct {
	Status InclusionListTxStatus
	Reason string // short human readable explanation
}

// InclusionListEvaluation holds the outcome for all inclusion lists of a slot.
type InclusionListEvaluation struct {
	// The beacon block at slot+1 whose execution payload must satisfy the lists.
	TargetSlot        phase0.Slot
	TargetBlockRoot   []byte
	TargetBlockNumber uint64
	TargetBlockHash   []byte
	TargetGasLeft     uint64
	TargetStatus      InclusionListTxStatus // Pending / NotEnforced / Unknown, only set when no payload could be evaluated

	// Per inclusion list, in input order.
	Lists []*InclusionListInfo

	// Keyed by transaction hash.
	Transactions map[common.Hash]*InclusionListTxEvaluation
}

// InclusionListInfo holds whether an inclusion list counts towards the satisfaction check.
type InclusionListInfo struct {
	SeenDelay    time.Duration // first seen by dora, relative to slot start
	Timely       bool          // seen before the inclusion list due time
	Equivocation bool          // the member published conflicting lists for the slot
}

type ilAccountStateKey struct {
	blockHash common.Hash
	address   common.Address
}

type ilAccountState struct {
	nonce   uint64
	balance *big.Int
}

var (
	ilAccountStateCacheOnce sync.Once
	ilAccountStateCache     *lru.Cache[ilAccountStateKey, *ilAccountState]
)

func getIlAccountStateCache() *lru.Cache[ilAccountStateKey, *ilAccountState] {
	ilAccountStateCacheOnce.Do(func() {
		ilAccountStateCache, _ = lru.New[ilAccountStateKey, *ilAccountState](10000)
	})
	return ilAccountStateCache
}

// EvaluateInclusionLists checks the inclusion lists of a slot against the execution
// payload of the canonical block at slot+1, which is the payload fork choice tests
// them against (heze fork-choice: record_payload_inclusion_list_satisfaction).
//
// Every transaction missing from that payload gets the reason the EIP-7805
// satisfaction check skipped it - not enough gas left, invalid nonce or balance
// against the payload's post-state - or ILTxStatusUnsatisfied when none applies.
//
// Like fork choice (get_inclusion_list_transactions with only_timely), lists seen after
// the inclusion list due time and lists of equivocating members are not enforced. The
// timeliness is judged from when dora first saw a list on its beacon event streams, which
// approximates but does not equal what the attesting nodes saw.
func (bs *ChainService) EvaluateInclusionLists(ctx context.Context, slot phase0.Slot, inclusionLists []*beacon.InclusionListEntry) *InclusionListEvaluation {
	result := &InclusionListEvaluation{
		TargetSlot:   slot + 1,
		Lists:        make([]*InclusionListInfo, len(inclusionLists)),
		Transactions: map[common.Hash]*InclusionListTxEvaluation{},
	}

	chainState := bs.consensusPool.GetChainState()
	slotStart := chainState.SlotToTime(slot)
	var dueDelay time.Duration
	if specs := chainState.GetSpecs(); specs != nil && specs.InclusionListDueBPS > 0 {
		dueDelay = time.Duration(specs.SlotDurationMs*specs.InclusionListDueBPS/10000) * time.Millisecond
	}

	listCount := make(map[phase0.ValidatorIndex]int)
	for _, entry := range inclusionLists {
		if entry != nil && entry.InclusionList != nil && entry.InclusionList.Message != nil {
			listCount[entry.InclusionList.Message.ValidatorIndex]++
		}
	}

	// Decode all inclusion list transactions once (deduplicated by hash, as in the spec),
	// remembering why a transaction is not enforced when no counting list carries it.
	ilTxs := make(map[common.Hash]*txtypes.Transaction)
	enforced := make(map[common.Hash]bool)
	notEnforcedReason := make(map[common.Hash]*InclusionListTxEvaluation)
	for idx, entry := range inclusionLists {
		info := &InclusionListInfo{Timely: true}
		result.Lists[idx] = info
		if entry == nil || entry.InclusionList == nil || entry.InclusionList.Message == nil {
			continue
		}
		il := entry.InclusionList

		info.SeenDelay = entry.SeenAt.Sub(slotStart)
		if dueDelay > 0 && info.SeenDelay >= dueDelay {
			info.Timely = false
		}
		info.Equivocation = listCount[il.Message.ValidatorIndex] > 1

		for _, txBytes := range il.Message.Transactions {
			tx, err := txtypes.DecodeTx(txBytes)
			if err != nil {
				continue
			}
			hash := tx.Hash()
			ilTxs[hash] = tx

			switch {
			case info.Equivocation:
				notEnforcedReason[hash] = &InclusionListTxEvaluation{
					Status: ILTxStatusEquivocation,
					Reason: fmt.Sprintf("Not enforced: validator %v published conflicting lists", il.Message.ValidatorIndex),
				}
			case !info.Timely:
				if notEnforcedReason[hash] == nil {
					notEnforcedReason[hash] = &InclusionListTxEvaluation{
						Status: ILTxStatusUntimely,
						Reason: fmt.Sprintf("Not enforced: list seen %.1fs into the slot, after the %.1fs due time", info.SeenDelay.Seconds(), dueDelay.Seconds()),
					}
				}
			default:
				enforced[hash] = true
			}
		}
	}

	setAll := func(status InclusionListTxStatus, reason string) *InclusionListEvaluation {
		result.TargetStatus = status
		for hash := range ilTxs {
			result.Transactions[hash] = &InclusionListTxEvaluation{Status: status, Reason: reason}
		}
		return result
	}

	// Locate the canonical block at slot+1.
	var targetBlock *dbtypes.Slot
	for _, block := range bs.GetDbBlocksForSlots(ctx, uint64(slot+1), 1, false, false) {
		if block.Slot == uint64(slot+1) && block.Status == dbtypes.Canonical {
			targetBlock = block
			break
		}
	}
	if targetBlock == nil {
		if chainState.CurrentSlot() <= slot+1 {
			return setAll(ILTxStatusPending, fmt.Sprintf("Waiting for the block at slot %v", slot+1))
		}
		return setAll(ILTxStatusNotEnforced, fmt.Sprintf("Not enforced: no block at slot %v", slot+1))
	}
	result.TargetBlockRoot = targetBlock.Root

	targetData, err := bs.GetSlotDetailsByBlockroot(ctx, phase0.Root(targetBlock.Root))
	if err != nil || targetData == nil || targetData.Block == nil {
		return setAll(ILTxStatusUnknown, fmt.Sprintf("Block at slot %v not available", slot+1))
	}

	payload := getCombinedBlockExecutionPayload(targetData)
	if payload == nil {
		if chainState.CurrentSlot() <= slot+2 {
			return setAll(ILTxStatusPending, fmt.Sprintf("Waiting for the payload of slot %v", slot+1))
		}
		return setAll(ILTxStatusNotEnforced, fmt.Sprintf("Not enforced: no payload for slot %v", slot+1))
	}

	result.TargetBlockNumber = payload.BlockNumber
	result.TargetBlockHash = payload.BlockHash[:]
	if payload.GasLimit > payload.GasUsed {
		result.TargetGasLeft = payload.GasLimit - payload.GasUsed
	}

	var baseFee *big.Int
	if payload.BaseFeePerGas != nil {
		baseFee = payload.BaseFeePerGas.ToBig()
	} else {
		baseFee = new(big.Int).SetUint64(utils.GetBaseFeeAsUint64(payload.BaseFeePerGasLE))
	}

	payloadTxs := make(map[common.Hash]bool, len(payload.Transactions))
	for _, txBytes := range payload.Transactions {
		if tx, err := txtypes.DecodeTx(txBytes); err == nil {
			payloadTxs[tx.Hash()] = true
		}
	}

	// Steps 1 & 2 of the satisfaction check need no state; collect senders for step 3.
	pending := make(map[common.Hash]common.Address)
	senders := make(map[common.Address]bool)
	for hash, tx := range ilTxs {
		switch {
		case payloadTxs[hash]:
			result.Transactions[hash] = &InclusionListTxEvaluation{
				Status: ILTxStatusIncluded,
				Reason: fmt.Sprintf("Included in slot %v", slot+1),
			}
		case !enforced[hash]:
			result.Transactions[hash] = notEnforcedReason[hash]
		case tx.Gas() > result.TargetGasLeft:
			result.Transactions[hash] = &InclusionListTxEvaluation{
				Status: ILTxStatusBlockFull,
				Reason: fmt.Sprintf("Tx gas limit %v exceeds the %v gas left in the block", tx.Gas(), result.TargetGasLeft),
			}
		default:
			if deadline, ok := tx.ExpiryDeadline(); ok && deadline < payload.Timestamp {
				result.Transactions[hash] = &InclusionListTxEvaluation{
					Status: ILTxStatusExpired,
					Reason: fmt.Sprintf("Expired at %v, block timestamp %v", deadline, payload.Timestamp),
				}
				continue
			}

			from, err := tx.From(tx.ChainId())
			if err != nil {
				result.Transactions[hash] = &InclusionListTxEvaluation{
					Status: ILTxStatusInvalid,
					Reason: fmt.Sprintf("Sender not recoverable (%v)", err),
				}
				continue
			}
			pending[hash] = from
			senders[from] = true
		}
	}

	if len(pending) == 0 {
		return result
	}

	accountStates := bs.getIlAccountStates(ctx, common.Hash(payload.BlockHash), payload.BlockNumber, senders)

	for hash, from := range pending {
		tx := ilTxs[hash]
		state := accountStates[from]
		eval := &InclusionListTxEvaluation{}
		result.Transactions[hash] = eval

		switch {
		case state == nil:
			eval.Status = ILTxStatusUnknown
			eval.Reason = "Sender state not available"
		case tx.UsesAccountNonce() && tx.Nonce() < state.nonce:
			eval.Status = ILTxStatusNonceTooLow
			eval.Reason = fmt.Sprintf("Tx nonce %v, account nonce %v", tx.Nonce(), state.nonce)
		case tx.UsesAccountNonce() && tx.Nonce() > state.nonce:
			eval.Status = ILTxStatusNonceTooHigh
			eval.Reason = fmt.Sprintf("Tx nonce %v, account nonce %v", tx.Nonce(), state.nonce)
		case tx.GasFeeCap() != nil && tx.GasFeeCap().Cmp(baseFee) < 0:
			eval.Status = ILTxStatusFeeCapTooLow
			eval.Reason = fmt.Sprintf("Max fee %v wei below base fee %v wei", tx.GasFeeCap(), baseFee)
		case state.balance != nil && tx.Cost().Cmp(state.balance) > 0:
			eval.Status = ILTxStatusInsufficientFunds
			eval.Reason = fmt.Sprintf("Costs up to %v wei, balance %v wei", tx.Cost(), state.balance)
		default:
			eval.Status = ILTxStatusUnsatisfied
			eval.Reason = fmt.Sprintf("Valid against the post-state and fits, but missing from the slot %v payload", slot+1)
		}
	}

	return result
}

// getCombinedBlockExecutionPayload returns the execution payload of a block, sourced
// from the payload envelope for gloas+ blocks.
func getCombinedBlockExecutionPayload(blockData *CombinedBlockResponse) *all.ExecutionPayload {
	if blockData.Block.Version >= spec.DataVersionGloas {
		if blockData.Payload != nil && blockData.Payload.Message != nil {
			return blockData.Payload.Message.Payload
		}
		return nil
	}
	if blockData.Block.Message != nil && blockData.Block.Message.Body != nil {
		return blockData.Block.Message.Body.ExecutionPayload
	}
	return nil
}

// getIlAccountStates fetches nonce and balance of the given accounts at the post-state
// of an execution block. Results are cached, as the post-state of a block never changes.
func (bs *ChainService) getIlAccountStates(ctx context.Context, blockHash common.Hash, blockNumber uint64, accounts map[common.Address]bool) map[common.Address]*ilAccountState {
	cache := getIlAccountStateCache()
	result := make(map[common.Address]*ilAccountState, len(accounts))

	missing := make([]common.Address, 0, len(accounts))
	for address := range accounts {
		if state, ok := cache.Get(ilAccountStateKey{blockHash, address}); ok {
			result[address] = state
		} else {
			missing = append(missing, address)
		}
	}
	if len(missing) == 0 {
		return result
	}

	clients := bs.executionPool.GetReadyEndpoints(execution.AnyClient)
	if len(clients) == 0 {
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	blockNum := new(big.Int).SetUint64(blockNumber)
	resultMutex := sync.Mutex{}
	workers := make(chan struct{}, 8)
	wg := sync.WaitGroup{}

	for _, address := range missing {
		wg.Add(1)
		workers <- struct{}{}
		go func(address common.Address) {
			defer func() {
				<-workers
				wg.Done()
			}()

			for _, client := range clients {
				ethClient := client.GetRPCClient().GetEthClient()
				if ethClient == nil {
					continue
				}

				// Prefer querying by hash (fork safe), fall back to the number for clients
				// without EIP-1898 support.
				nonce, err := ethClient.NonceAtHash(ctx, address, blockHash)
				byHash := err == nil
				if !byHash {
					nonce, err = ethClient.NonceAt(ctx, address, blockNum)
				}
				if err != nil {
					logrus.Debugf("inclusion list eval: nonce lookup for %v at %v on %v failed: %v", address.Hex(), blockNumber, client.GetName(), err)
					continue
				}
				var balance *big.Int
				if byHash {
					balance, err = ethClient.BalanceAtHash(ctx, address, blockHash)
				} else {
					balance, err = ethClient.BalanceAt(ctx, address, blockNum)
				}
				if err != nil {
					logrus.Debugf("inclusion list eval: balance lookup for %v at %v on %v failed: %v", address.Hex(), blockNumber, client.GetName(), err)
					continue
				}

				state := &ilAccountState{nonce: nonce, balance: balance}
				cache.Add(ilAccountStateKey{blockHash, address}, state)

				resultMutex.Lock()
				result[address] = state
				resultMutex.Unlock()
				return
			}
		}(address)
	}
	wg.Wait()

	return result
}
