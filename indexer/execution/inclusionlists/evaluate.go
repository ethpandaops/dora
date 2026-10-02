package inclusionlists

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"

	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

// weiPerGwei converts withdrawal amounts to wei.
var weiPerGwei = big.NewInt(1_000_000_000)

// evaluationInput holds everything the satisfaction check of a slot's
// inclusion lists against one execution payload needs besides sender state.
type evaluationInput struct {
	// lists are the inclusion lists published in the slot before the target block.
	lists []*beacon.InclusionListObservation
	// dueMs is the inclusion list due time as ms offset into the slot (0 = unknown).
	dueMs int32
	// dependentRoot is the committee shuffling dependent root on the target
	// block's chain. Valid if hasDependentRoot is set.
	dependentRoot    phase0.Root
	hasDependentRoot bool
	// blockRoot and payload identify the target block and its execution payload.
	blockRoot phase0.Root
	payload   *all.ExecutionPayload
	// parentTransactions holds the raw transactions of the payload the target
	// payload builds on.
	parentTransactions map[string]bool
}

// senderState is the state of a transaction sender at the target payload's
// post-state.
type senderState struct {
	nonce   uint64
	balance *big.Int
	// hasCode is set if the sender has non-delegated code. Only valid if
	// codeKnown is set.
	hasCode   bool
	codeKnown bool
}

// evaluation is an in-progress satisfaction check. It follows the EIP-7805
// check an execution client runs on a new payload: every inclusion list
// transaction that is missing from the payload must either not fit into the
// remaining gas or be invalid against the payload's post-state.
type evaluation struct {
	input    *evaluationInput
	fragment *btypes.SlotInclusionLists
	result   *btypes.SlotInclusionListEval
	gasLeft  uint64
	// transactions holds the decoded transaction per table entry (nil if not decodable).
	transactions []*txtypes.Transaction
	// pending maps the table index of every transaction whose outcome depends
	// on the sender state to its sender.
	pending map[int]common.Address
}

// newEvaluation runs all checks that need no sender state. Transactions left
// undecided are listed in pending and decided by applySenderStates.
func newEvaluation(input *evaluationInput) *evaluation {
	payload := input.payload

	result := &btypes.SlotInclusionListEval{
		BlockRoot:   input.blockRoot,
		BlockHash:   payload.BlockHash,
		BlockNumber: payload.BlockNumber,
		GasLimit:    payload.GasLimit,
		GasUsed:     payload.GasUsed,
		Timestamp:   payload.Timestamp,
		ListFlags:   make([]uint8, 0, len(input.lists)),
	}
	if payload.BaseFeePerGas != nil {
		result.BaseFee = utils.GetBaseFeeAsUint64(payload.BaseFeePerGas)
	} else {
		result.BaseFee = utils.GetBaseFeeAsUint64(payload.BaseFeePerGasLE)
	}

	fragment := &btypes.SlotInclusionLists{
		TxHashes:     make([][32]byte, 0, 64),
		Transactions: make([][]byte, 0, 64),
		Lists:        make([]*btypes.SlotInclusionList, 0, len(input.lists)),
		Evals:        []*btypes.SlotInclusionListEval{result},
	}

	eval := &evaluation{
		input:    input,
		fragment: fragment,
		result:   result,
		pending:  make(map[int]common.Address, 16),
	}
	if payload.GasLimit > payload.GasUsed {
		eval.gasLeft = payload.GasLimit - payload.GasUsed
	}

	listCount := make(map[phase0.ValidatorIndex]int, len(input.lists))
	for _, list := range input.lists {
		listCount[list.InclusionList.Message.ValidatorIndex]++
	}

	// Build the transaction table and remember, per transaction, the weakest
	// reason a list carrying it is not enforced (0 = enforced by some list).
	txIdx := make(map[string]uint16, 64)
	notEnforced := make([]uint8, 0, 64)
	for _, list := range input.lists {
		message := list.InclusionList.Message

		flags := btypes.ILListFlagEvaluated
		if input.dueMs > 0 && list.HasSeen && list.FirstSeen >= input.dueMs {
			flags |= btypes.ILListFlagLate
		}
		if listCount[message.ValidatorIndex] > 1 {
			flags |= btypes.ILListFlagEquivocation
		}
		if input.hasDependentRoot && message.DependentRoot != input.dependentRoot {
			flags |= btypes.ILListFlagWrongBranch
		}
		result.ListFlags = append(result.ListFlags, flags)

		reason := listNotEnforcedStatus(flags)
		entry := &btypes.SlotInclusionList{
			ValidatorIndex: uint64(message.ValidatorIndex),
			DependentRoot:  message.DependentRoot,
			Signature:      list.InclusionList.Signature,
			TxRefs:         make([]uint16, 0, len(message.Transactions)),
		}
		for _, rawTx := range message.Transactions {
			idx, ok := txIdx[string(rawTx)]
			if !ok {
				if len(fragment.Transactions) >= 0xffff {
					continue
				}
				idx = uint16(len(fragment.Transactions))
				txIdx[string(rawTx)] = idx
				fragment.Transactions = append(fragment.Transactions, rawTx)
				fragment.TxHashes = append(fragment.TxHashes, beacon.InclusionListTxHash(rawTx))
				notEnforced = append(notEnforced, reason)
			} else if reason == 0 || (notEnforced[idx] != 0 && reason < notEnforced[idx]) {
				notEnforced[idx] = reason
			}
			entry.TxRefs = append(entry.TxRefs, idx)
		}
		fragment.Lists = append(fragment.Lists, entry)
	}

	payloadTransactions := make(map[string]bool, len(payload.Transactions))
	for _, rawTx := range payload.Transactions {
		payloadTransactions[string(rawTx)] = true
	}

	result.Txs = make([]btypes.SlotInclusionListTx, len(fragment.Transactions))
	eval.transactions = make([]*txtypes.Transaction, len(fragment.Transactions))
	for idx, rawTx := range fragment.Transactions {
		status, sender := eval.checkTransaction(idx, rawTx, payloadTransactions, notEnforced[idx])
		if status == btypes.ILTxStatusUnknown {
			eval.pending[idx] = sender
			continue
		}
		result.Txs[idx].Status = status
	}

	return eval
}

// listNotEnforcedStatus returns the transaction status for a list that is not
// enforced, or 0 if the list is enforced.
func listNotEnforcedStatus(flags uint8) uint8 {
	switch {
	case flags&btypes.ILListFlagEquivocation != 0:
		return btypes.ILTxStatusListEquivocation
	case flags&btypes.ILListFlagWrongBranch != 0:
		return btypes.ILTxStatusListWrongBranch
	case flags&btypes.ILListFlagLate != 0:
		return btypes.ILTxStatusListLate
	default:
		return 0
	}
}

// checkTransaction decides the outcome of a transaction as far as possible
// without sender state. It returns ILTxStatusUnknown and the sender if the
// outcome depends on the sender state.
func (eval *evaluation) checkTransaction(idx int, rawTx []byte, payloadTransactions map[string]bool, notEnforced uint8) (uint8, common.Address) {
	payload := eval.input.payload

	switch {
	case payloadTransactions[string(rawTx)]:
		return btypes.ILTxStatusIncluded, common.Address{}
	case eval.input.parentTransactions[string(rawTx)]:
		return btypes.ILTxStatusIncludedEarlier, common.Address{}
	case notEnforced != 0:
		return notEnforced, common.Address{}
	}

	tx, err := txtypes.DecodeTx(rawTx)
	if err != nil {
		return btypes.ILTxStatusMalformed, common.Address{}
	}
	eval.transactions[idx] = tx

	switch {
	case tx.Type() == txtypes.FrameTxType || !tx.UsesAccountNonce():
		// The check judges appendability on the account nonce, which a frame
		// transaction does not use.
		return btypes.ILTxStatusFrameTx, common.Address{}
	case tx.Type() == txtypes.BlobTxType:
		return btypes.ILTxStatusBlobTx, common.Address{}
	case tx.Gas() > eval.gasLeft:
		return btypes.ILTxStatusGasLimit, common.Address{}
	case tx.GasFeeCap() != nil && tx.GasFeeCap().Cmp(new(big.Int).SetUint64(eval.result.BaseFee)) < 0:
		return btypes.ILTxStatusFeeCapTooLow, common.Address{}
	}

	if deadline, ok := tx.ExpiryDeadline(); ok && deadline < payload.Timestamp {
		return btypes.ILTxStatusExpired, common.Address{}
	}

	sender, err := tx.From(tx.ChainId())
	if err != nil {
		return btypes.ILTxStatusMalformed, common.Address{}
	}

	return btypes.ILTxStatusUnknown, sender
}

// senders returns the distinct senders whose state is needed.
func (eval *evaluation) senders() []common.Address {
	seen := make(map[common.Address]bool, len(eval.pending))
	senders := make([]common.Address, 0, len(eval.pending))
	for _, sender := range eval.pending {
		if !seen[sender] {
			seen[sender] = true
			senders = append(senders, sender)
		}
	}
	return senders
}

// codeCheckSenders returns the senders whose transactions pass the nonce and
// balance checks, so that the outcome depends on whether they have code.
func (eval *evaluation) codeCheckSenders(states map[common.Address]*senderState) []common.Address {
	seen := make(map[common.Address]bool, 4)
	senders := make([]common.Address, 0, 4)
	for idx, sender := range eval.pending {
		state := states[sender]
		if state == nil || state.codeKnown || seen[sender] {
			continue
		}
		if eval.checkSenderState(idx, sender, state) == btypes.ILTxStatusUnsatisfied {
			seen[sender] = true
			senders = append(senders, sender)
		}
	}
	return senders
}

// applySenderStates decides all pending transactions from the given sender
// states. Transactions of senders without state stay unknown.
func (eval *evaluation) applySenderStates(states map[common.Address]*senderState) {
	for idx, sender := range eval.pending {
		state := states[sender]
		if state == nil {
			continue
		}

		tx := &eval.result.Txs[idx]
		tx.Status = eval.checkSenderState(idx, sender, state)
		tx.Flags = btypes.ILTxFlagHasState
		tx.Nonce = state.nonce
		tx.Balance = state.balance.Bytes()
		if state.hasCode {
			tx.Flags |= btypes.ILTxFlagSenderHasCode
		}
	}
}

// checkSenderState validates a transaction against its sender's state.
func (eval *evaluation) checkSenderState(idx int, sender common.Address, state *senderState) uint8 {
	tx := eval.transactions[idx]

	switch {
	case tx.Nonce() < state.nonce:
		return btypes.ILTxStatusNonceTooLow
	case tx.Nonce() > state.nonce:
		return btypes.ILTxStatusNonceTooHigh
	case tx.Cost().Cmp(eval.spendableBalance(sender, state.balance)) > 0:
		return btypes.ILTxStatusInsufficientFunds
	case state.hasCode:
		return btypes.ILTxStatusSenderHasCode
	default:
		return btypes.ILTxStatusUnsatisfied
	}
}

// spendableBalance returns the balance the sender would have had when an
// appended transaction executed. Withdrawals are credited after the payload's
// transactions, so they are not part of it.
func (eval *evaluation) spendableBalance(sender common.Address, balance *big.Int) *big.Int {
	spendable := balance
	for _, withdrawal := range eval.input.payload.Withdrawals {
		if withdrawal == nil || common.Address(withdrawal.Address) != sender {
			continue
		}

		amount := new(big.Int).SetUint64(uint64(withdrawal.Amount))
		spendable = new(big.Int).Sub(spendable, amount.Mul(amount, weiPerGwei))
	}

	if spendable.Sign() < 0 {
		return new(big.Int)
	}

	return spendable
}
