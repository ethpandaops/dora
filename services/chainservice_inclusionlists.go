package services

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/dbtypes"
)

// SlotInclusionListsView describes the inclusion lists (EIP-7805) published
// in a slot: the committee, the lists with their gossip observations and how
// the execution payloads of the following slot treated their transactions.
type SlotInclusionListsView struct {
	// Slot is the slot the lists were published in.
	Slot uint64 `json:"slot"`
	// TargetSlot is the slot whose execution payload the lists constrain.
	TargetSlot uint64 `json:"target_slot"`
	// TargetState tells why Targets is empty: "pending" while the target slot
	// has not passed, "missed" if it has no block. Empty otherwise.
	TargetState string `json:"target_state,omitempty"`
	// DueMs is the inclusion list due time as ms offset into the slot. Lists
	// first seen after it are not enforced.
	DueMs uint64 `json:"due_ms"`
	// Clients holds the observer client names; observations reference them by index.
	Clients      []string                        `json:"clients"`
	Committee    []*SlotInclusionListMember      `json:"committee"`
	Lists        []*SlotInclusionListEntry       `json:"lists"`
	Transactions []*SlotInclusionListTransaction `json:"transactions"`
	Targets      []*SlotInclusionListTarget      `json:"targets"`
}

// SlotInclusionListMember is one member of the inclusion list committee.
type SlotInclusionListMember struct {
	ValidatorIndex uint64 `json:"validator_index"`
	ValidatorName  string `json:"validator_name,omitempty"`
	// Status is "submitted", "late", "equivocated" or "missing".
	Status string `json:"status"`
	// Lists holds the indexes of the member's lists in SlotInclusionListsView.Lists.
	Lists []int `json:"lists"`
	// FirstSeen is the earliest observation of a list of the member as ms
	// offset from slot start.
	FirstSeen *int32 `json:"first_seen,omitempty"`
}

// SlotInclusionListEntry is one signed inclusion list.
type SlotInclusionListEntry struct {
	ValidatorIndex uint64 `json:"validator_index"`
	ValidatorName  string `json:"validator_name,omitempty"`
	DependentRoot  string `json:"dependent_root"`
	Signature      string `json:"signature"`
	// Transactions holds the list's transactions in list order as indexes
	// into SlotInclusionListsView.Transactions.
	Transactions []int `json:"transactions"`
	// FirstSeen is the earliest observation as ms offset from slot start.
	FirstSeen *int32                     `json:"first_seen,omitempty"`
	Seen      []*SlotInclusionListSeenBy `json:"seen"`
}

// SlotInclusionListSeenBy is one client's first sighting of an inclusion list.
type SlotInclusionListSeenBy struct {
	// Client is an index into SlotInclusionListsView.Clients.
	Client int `json:"client"`
	// Time is the first-seen offset in ms from slot start.
	Time int32 `json:"time"`
}

// SlotInclusionListTransaction is one unique transaction of a slot's inclusion
// lists. Only Hash is set if the transaction details were not requested; a
// transaction that is not decodable has no details besides Raw.
type SlotInclusionListTransaction struct {
	Hash string `json:"hash"`
	// Raw is the encoded transaction as carried by the lists.
	Raw          string `json:"raw,omitempty"`
	Decoded      bool   `json:"decoded"`
	Type         uint8  `json:"type"`
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	Value        string `json:"value,omitempty"`
	Nonce        uint64 `json:"nonce"`
	GasLimit     uint64 `json:"gas_limit"`
	MaxFeePerGas string `json:"max_fee_per_gas,omitempty"`
	DataLen      uint64 `json:"data_len"`
}

// SlotInclusionListTarget is one block of the target slot with the outcome of
// the satisfaction check of the slot's lists against its execution payload.
type SlotInclusionListTarget struct {
	BlockRoot string `json:"block_root"`
	// Status is "canonical" or "orphaned".
	Status string `json:"status"`
	// PayloadStatus is "canonical" if the chain built on the block's payload,
	// "orphaned" if it did not and "missing" if the payload was never revealed.
	// Fork choice does not extend a payload that leaves the lists unsatisfied,
	// so this is how the network judged the payload.
	PayloadStatus string `json:"payload_status"`
	// Evaluated is false if the lists have not been checked against the block.
	Evaluated   bool   `json:"evaluated"`
	BlockHash   string `json:"block_hash,omitempty"`
	BlockNumber uint64 `json:"block_number"`
	GasLeft     uint64 `json:"gas_left"`
	BaseFee     uint64 `json:"base_fee"`
	Unsatisfied uint64 `json:"unsatisfied"`
	// Lists holds, per list, why the list is not enforced against this block:
	// "late", "equivocation" or "wrong_branch", or "not_evaluated" if the
	// evaluation does not cover the list. Empty if it is enforced.
	Lists []string `json:"lists"`
	// Transactions holds the outcome per entry of SlotInclusionListsView.Transactions.
	Transactions []*SlotInclusionListOutcome `json:"transactions"`
	// BidAcknowledged holds, per committee member, whether the block's
	// execution payload bid acknowledges a list of the member
	// (inclusion_list_bits). Nil if the bid is not available.
	BidAcknowledged []bool `json:"bid_acknowledged,omitempty"`
}

// SlotInclusionListOutcome is the outcome of one inclusion list transaction
// against one target block.
type SlotInclusionListOutcome struct {
	// Status is the persisted status code (blockdb ILTxStatus*).
	Status uint8 `json:"status"`
	// Group is "included", "not_enforced", "omitted" (validly left out),
	// "unsatisfied" (inclusion list violation) or "unknown".
	Group  string `json:"group"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

// inclusionListStatusInfo maps a status code to its display group and label.
var inclusionListStatusInfo = map[uint8][2]string{
	btypes.ILTxStatusUnknown:           {"unknown", "Unknown"},
	btypes.ILTxStatusIncluded:          {"included", "Included"},
	btypes.ILTxStatusIncludedEarlier:   {"included", "Included earlier"},
	btypes.ILTxStatusListLate:          {"not_enforced", "Late list"},
	btypes.ILTxStatusListEquivocation:  {"not_enforced", "Equivocation"},
	btypes.ILTxStatusListWrongBranch:   {"not_enforced", "Other branch"},
	btypes.ILTxStatusFrameTx:           {"not_enforced", "Frame tx"},
	btypes.ILTxStatusGasLimit:          {"omitted", "Block full"},
	btypes.ILTxStatusMalformed:         {"omitted", "Malformed"},
	btypes.ILTxStatusFeeCapTooLow:      {"omitted", "Fee too low"},
	btypes.ILTxStatusNonceTooLow:       {"omitted", "Nonce too low"},
	btypes.ILTxStatusNonceTooHigh:      {"omitted", "Nonce gap"},
	btypes.ILTxStatusInsufficientFunds: {"omitted", "Insufficient funds"},
	btypes.ILTxStatusSenderHasCode:     {"omitted", "Sender has code"},
	btypes.ILTxStatusExpired:           {"omitted", "Expired"},
	btypes.ILTxStatusBlobTx:            {"omitted", "Blob tx"},
	btypes.ILTxStatusUnsatisfied:       {"unsatisfied", "Unsatisfied"},
}

// GetSlotInclusionLists returns the inclusion lists published in the given
// slot with their committee, observations and evaluations, from the live
// cache for recent slots and the blockdb meta object otherwise. The raw
// transactions are only loaded if withTransactions is set. Returns nil if no
// lists are known for the slot.
func (bs *ChainService) GetSlotInclusionLists(ctx context.Context, slot phase0.Slot, withTransactions bool) *btypes.SlotMeta {
	if cached := bs.beaconIndexer.GetSlotInclusionLists(slot); cached != nil {
		return cached
	}

	if !blockdb.GlobalBlockDb.SupportsSlotMeta() {
		return nil
	}

	flags := btypes.SlotMetaFlagInclusionLists
	if withTransactions {
		flags |= btypes.SlotMetaFlagInclusionTxs
	}

	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(ctx, uint64(slot), flags)
	if err != nil {
		bs.logger.Warnf("error loading meta object for slot %d: %v", slot, err)
		return nil
	}
	if stored == nil || stored.InclusionLists == nil {
		return nil
	}

	return stored
}

// getInclusionListCommittee returns the inclusion list committee of a slot:
// the one stored with the lists if available, otherwise the first
// INCLUSION_LIST_COMMITTEE_SIZE members of the slot's concatenated beacon
// committees.
func (bs *ChainService) getInclusionListCommittee(ctx context.Context, slot phase0.Slot, lists *btypes.SlotInclusionLists) []uint64 {
	if lists != nil && len(lists.Lists) > 0 {
		if committee := lists.GetCommittee(lists.Lists[0].DependentRoot); committee != nil {
			return committee.Members
		}
	}
	if committee := bs.beaconIndexer.GetInclusionListCommittee(slot); committee != nil {
		return committee.Members
	}

	specs := bs.consensusPool.GetChainState().GetSpecs()
	if specs == nil || specs.InclusionListCommitteeSize == 0 {
		return nil
	}

	members := make([]uint64, 0, 256)
	for _, committee := range bs.GetSlotCommittees(ctx, slot) {
		for _, member := range committee {
			members = append(members, uint64(member))
		}
	}
	if len(members) == 0 {
		return nil
	}

	committee := make([]uint64, 0, specs.InclusionListCommitteeSize)
	for i := range specs.InclusionListCommitteeSize {
		committee = append(committee, members[int(i)%len(members)])
	}

	return committee
}

// GetSlotInclusionListsView assembles the view of the inclusion lists
// published in the given slot. Transaction details and the bid cross-check are
// only resolved if withDetails is set; without it the stored raw transactions
// and the target blocks are not loaded.
func (bs *ChainService) GetSlotInclusionListsView(ctx context.Context, slot phase0.Slot, withDetails bool) *SlotInclusionListsView {
	chainState := bs.consensusPool.GetChainState()
	specs := chainState.GetSpecs()

	view := &SlotInclusionListsView{
		Slot:         uint64(slot),
		TargetSlot:   uint64(slot) + 1,
		Clients:      []string{},
		Committee:    []*SlotInclusionListMember{},
		Lists:        []*SlotInclusionListEntry{},
		Transactions: []*SlotInclusionListTransaction{},
		Targets:      []*SlotInclusionListTarget{},
	}
	if specs != nil {
		view.DueMs = uint64(chainState.GetSlotDuration(slot).Milliseconds()) * specs.InclusionListDueBPS / 10000
	}

	var lists *btypes.SlotInclusionLists
	if obj := bs.GetSlotInclusionLists(ctx, slot, withDetails); obj != nil {
		lists = obj.InclusionLists
		view.Clients = obj.Clients
	}

	// Lists
	memberLists := make(map[uint64][]int, 16)
	if lists != nil {
		for idx, list := range lists.Lists {
			entry := &SlotInclusionListEntry{
				ValidatorIndex: list.ValidatorIndex,
				ValidatorName:  bs.GetValidatorNameAt(list.ValidatorIndex, slot),
				DependentRoot:  fmt.Sprintf("0x%x", list.DependentRoot[:]),
				Signature:      fmt.Sprintf("0x%x", list.Signature[:]),
				Transactions:   make([]int, 0, len(list.TxRefs)),
				Seen:           make([]*SlotInclusionListSeenBy, 0, len(list.SeenTimes)),
			}
			for _, ref := range list.TxRefs {
				entry.Transactions = append(entry.Transactions, int(ref))
			}
			for clientIdx, seenTime := range list.SeenByClientIndex() {
				entry.Seen = append(entry.Seen, &SlotInclusionListSeenBy{Client: clientIdx, Time: seenTime})
			}
			if first, ok := list.FirstSeen(); ok {
				entry.FirstSeen = &first
			}

			view.Lists = append(view.Lists, entry)
			memberLists[list.ValidatorIndex] = append(memberLists[list.ValidatorIndex], idx)
		}
	}

	// Committee
	committee := bs.getInclusionListCommittee(ctx, slot, lists)
	for _, validatorIndex := range committee {
		member := &SlotInclusionListMember{
			ValidatorIndex: validatorIndex,
			ValidatorName:  bs.GetValidatorNameAt(validatorIndex, slot),
			Status:         "missing",
			Lists:          memberLists[validatorIndex],
		}
		if member.Lists == nil {
			member.Lists = []int{}
		}

		for _, listIdx := range member.Lists {
			if first := view.Lists[listIdx].FirstSeen; first != nil && (member.FirstSeen == nil || *first < *member.FirstSeen) {
				member.FirstSeen = first
			}
		}
		switch {
		case len(member.Lists) > 1:
			member.Status = "equivocated"
		case len(member.Lists) == 1 && member.FirstSeen != nil && view.DueMs > 0 && int64(*member.FirstSeen) >= int64(view.DueMs):
			member.Status = "late"
		case len(member.Lists) == 1:
			member.Status = "submitted"
		}

		view.Committee = append(view.Committee, member)
	}

	if lists == nil {
		return view
	}

	// Transactions
	decoded := make([]*txtypes.Transaction, len(lists.TxHashes))
	for idx, hash := range lists.TxHashes {
		txView := &SlotInclusionListTransaction{
			Hash: fmt.Sprintf("0x%x", hash[:]),
		}
		if idx < len(lists.Transactions) {
			txView.Raw = fmt.Sprintf("0x%x", lists.Transactions[idx])
			txView.DataLen = uint64(len(lists.Transactions[idx]))
			if tx, err := txtypes.DecodeTx(lists.Transactions[idx]); err == nil {
				decoded[idx] = tx
				fillInclusionListTransaction(txView, tx)
			}
		}
		view.Transactions = append(view.Transactions, txView)
	}

	// Targets. The payloads of this slot's own blocks are remembered to tell
	// whether a target builds on one of them.
	targetBlocks := make([]*dbtypes.Slot, 0, 1)
	slotPayloads := make(map[string]bool, 1)
	for _, block := range bs.GetDbBlocksForSlots(ctx, view.TargetSlot, 1, false, true) {
		switch {
		case block.Slot == view.TargetSlot && len(block.Root) == 32:
			targetBlocks = append(targetBlocks, block)
		case block.Slot == view.Slot && len(block.EthBlockHash) == 32:
			slotPayloads[string(block.EthBlockHash)] = true
		}
	}
	if len(targetBlocks) == 0 {
		if chainState.CurrentSlot() <= slot+1 {
			view.TargetState = "pending"
		} else {
			view.TargetState = "missed"
		}
	}

	for _, block := range targetBlocks {
		target := &SlotInclusionListTarget{
			BlockRoot:     fmt.Sprintf("0x%x", block.Root),
			Status:        "canonical",
			PayloadStatus: "canonical",
			Lists:         make([]string, len(lists.Lists)),
			Transactions:  make([]*SlotInclusionListOutcome, 0, len(lists.TxHashes)),
		}
		if block.Status == dbtypes.Orphaned {
			target.Status = "orphaned"
		}
		switch block.PayloadStatus {
		case dbtypes.PayloadStatusOrphaned:
			target.PayloadStatus = "orphaned"
		case dbtypes.PayloadStatusMissing:
			target.PayloadStatus = "missing"
		}

		// covered marks the transactions carried by a list the evaluation
		// covers. A list that was first seen after the payload was checked
		// is not part of it.
		covered := make([]bool, len(lists.TxHashes))

		eval := lists.GetEval(phase0.Root(block.Root))
		if eval != nil {
			target.Evaluated = true
			target.BlockHash = fmt.Sprintf("0x%x", eval.BlockHash[:])
			target.BlockNumber = eval.BlockNumber
			target.BaseFee = eval.BaseFee
			if eval.GasLimit > eval.GasUsed {
				target.GasLeft = eval.GasLimit - eval.GasUsed
			}

			for idx, list := range lists.Lists {
				var flags uint8
				if idx < len(eval.ListFlags) {
					flags = eval.ListFlags[idx]
				}
				if flags&btypes.ILListFlagEvaluated != 0 {
					for _, ref := range list.TxRefs {
						if int(ref) < len(covered) {
							covered[ref] = true
						}
					}
				}

				switch {
				case flags&btypes.ILListFlagEvaluated == 0:
					target.Lists[idx] = "not_evaluated"
				case flags&btypes.ILListFlagEquivocation != 0:
					target.Lists[idx] = "equivocation"
				case flags&btypes.ILListFlagWrongBranch != 0:
					target.Lists[idx] = "wrong_branch"
				case flags&btypes.ILListFlagLate != 0:
					target.Lists[idx] = "late"
				}
			}
		}

		for idx := range lists.TxHashes {
			var txEval btypes.SlotInclusionListTx
			if eval != nil && idx < len(eval.Txs) {
				txEval = eval.Txs[idx]
			}

			outcome := bs.buildInclusionListOutcome(view, target, eval, &txEval, decoded[idx])
			if txEval.Status == btypes.ILTxStatusUnknown && eval != nil && !covered[idx] {
				outcome.Label = "Not evaluated"
				outcome.Reason = "Only in lists seen after the payload was checked"
			}
			if txEval.Status == btypes.ILTxStatusIncludedEarlier && slotPayloads[string(block.EthBlockParentHash)] {
				// The target builds on this slot's own payload, which the
				// lists were published alongside.
				outcome.Reason = fmt.Sprintf("Already included in the slot %d payload", view.Slot)
			}
			if outcome.Group == "unsatisfied" {
				target.Unsatisfied++
			}
			target.Transactions = append(target.Transactions, outcome)
		}

		if withDetails {
			target.BidAcknowledged = bs.getInclusionListBidBits(ctx, phase0.Root(block.Root), len(committee))
		}

		view.Targets = append(view.Targets, target)
	}

	return view
}

// fillInclusionListTransaction fills the details of a decoded transaction.
func fillInclusionListTransaction(txView *SlotInclusionListTransaction, tx *txtypes.Transaction) {
	txView.Decoded = true
	txView.Type = tx.Type()
	txView.Nonce = tx.Nonce()
	txView.GasLimit = tx.Gas()
	if to := tx.To(); to != nil {
		txView.To = to.Hex()
	}
	if value := tx.Value(); value != nil {
		txView.Value = value.String()
	}
	if feeCap := tx.GasFeeCap(); feeCap != nil {
		txView.MaxFeePerGas = feeCap.String()
	}
	if from, err := tx.From(tx.ChainId()); err == nil {
		txView.From = from.Hex()
	}
}

// buildInclusionListOutcome renders the outcome of one transaction against a
// target block. tx is nil if the transaction details are not available.
func (bs *ChainService) buildInclusionListOutcome(view *SlotInclusionListsView, target *SlotInclusionListTarget, eval *btypes.SlotInclusionListEval, txEval *btypes.SlotInclusionListTx, tx *txtypes.Transaction) *SlotInclusionListOutcome {
	info, ok := inclusionListStatusInfo[txEval.Status]
	if !ok {
		info = [2]string{"unknown", fmt.Sprintf("Status %d", txEval.Status)}
	}

	outcome := &SlotInclusionListOutcome{
		Status: txEval.Status,
		Group:  info[0],
		Label:  info[1],
	}

	hasState := txEval.Flags&btypes.ILTxFlagHasState != 0
	balance := new(big.Int).SetBytes(txEval.Balance)

	switch txEval.Status {
	case btypes.ILTxStatusUnknown:
		if eval == nil {
			outcome.Reason = "Not evaluated against this block"
		} else {
			outcome.Reason = "Sender state was not available"
		}
	case btypes.ILTxStatusIncluded:
		outcome.Reason = fmt.Sprintf("Included in slot %d payload", view.TargetSlot)
	case btypes.ILTxStatusIncludedEarlier:
		outcome.Reason = "Already included in an earlier payload"
	case btypes.ILTxStatusListLate:
		outcome.Reason = fmt.Sprintf("Only in lists seen after the %.1fs due time", float64(view.DueMs)/1000)
	case btypes.ILTxStatusListEquivocation:
		outcome.Reason = "Only in lists of equivocating members"
	case btypes.ILTxStatusListWrongBranch:
		outcome.Reason = "Only in lists for another committee shuffling"
	case btypes.ILTxStatusFrameTx:
		outcome.Reason = "Frame transactions are exempt from the check"
	case btypes.ILTxStatusBlobTx:
		outcome.Reason = "Blob transactions cannot be appended to a payload"
	case btypes.ILTxStatusMalformed:
		outcome.Reason = "Not decodable or sender not recoverable"
	case btypes.ILTxStatusGasLimit:
		if tx != nil {
			outcome.Reason = fmt.Sprintf("Gas limit %d exceeds the %d gas left", tx.Gas(), target.GasLeft)
		} else {
			outcome.Reason = fmt.Sprintf("Gas limit exceeds the %d gas left", target.GasLeft)
		}
	case btypes.ILTxStatusFeeCapTooLow:
		if tx != nil && tx.GasFeeCap() != nil {
			outcome.Reason = fmt.Sprintf("Max fee %v wei below base fee %d wei", tx.GasFeeCap(), target.BaseFee)
		} else {
			outcome.Reason = fmt.Sprintf("Max fee below base fee %d wei", target.BaseFee)
		}
	case btypes.ILTxStatusExpired:
		outcome.Reason = "Expiry deadline passed before the payload's timestamp"
	case btypes.ILTxStatusNonceTooLow, btypes.ILTxStatusNonceTooHigh:
		switch {
		case tx != nil && hasState:
			outcome.Reason = fmt.Sprintf("Tx nonce %d, account nonce %d", tx.Nonce(), txEval.Nonce)
		case hasState:
			outcome.Reason = fmt.Sprintf("Account nonce %d", txEval.Nonce)
		}
	case btypes.ILTxStatusInsufficientFunds:
		switch {
		case tx != nil && hasState:
			outcome.Reason = fmt.Sprintf("Costs up to %v wei, balance %v wei", tx.Cost(), balance)
		case hasState:
			outcome.Reason = fmt.Sprintf("Balance %v wei", balance)
		}
	case btypes.ILTxStatusSenderHasCode:
		outcome.Reason = "Sender has non-delegated code (EIP-3607)"
	case btypes.ILTxStatusUnsatisfied:
		outcome.Reason = "Valid and fitting, but missing from the payload"
	}

	return outcome
}

// getInclusionListBidBits returns, per committee member, whether the
// execution payload bid of the given block acknowledges a list of the member.
// Returns nil if the block or its bid is not available.
func (bs *ChainService) getInclusionListBidBits(ctx context.Context, blockRoot phase0.Root, committeeSize int) []bool {
	blockData, err := bs.GetSlotDetailsByBlockroot(ctx, blockRoot)
	if err != nil || blockData == nil || blockData.Block == nil || blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		return nil
	}

	bid := blockData.Block.Message.Body.SignedExecutionPayloadBid
	if bid == nil || bid.Message == nil || len(bid.Message.InclusionListBits) == 0 {
		return nil
	}

	bits := bid.Message.InclusionListBits
	acknowledged := make([]bool, committeeSize)
	for i := range committeeSize {
		if i>>3 < len(bits) {
			acknowledged[i] = bits[i>>3]&(1<<(i&7)) != 0
		}
	}

	return acknowledged
}
