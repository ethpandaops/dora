// Package types: inclusion list sections of the per-slot meta object.
//
// The object of slot S holds the inclusion lists (EIP-7805) published by the
// inclusion list committee of slot S. They constrain the execution payload of
// slot S+1, so their evaluations refer to the blocks of slot S+1. Observation
// offsets are relative to the start of slot S.
//
// The lists are stored in five sections of the meta object:
//
// INCLUSION COMMITTEE section: uint8 count, per committee (one per shuffling):
//
//	├── DependentRoot: 32 bytes
//	├── MemberCount:   uint16
//	└── Members:       uint64 validator index per member, in committee order
//
// INCLUSION LISTS section: uint16 count, per list: uint32 record length + record:
//
//	├── ValidatorIndex: uint64
//	├── DependentRoot:  32 bytes
//	├── Signature:      96 bytes
//	├── TxCount:        uint16
//	├── TxRefs:         uint16 per transaction (index into the transaction table)
//	├── SeenMask:       ceil(ClientCount/8) bytes (bit i = client table index i)
//	└── SeenTimes:      int32 per set mask bit, in ascending client index order
//
// INCLUSION TX HASHES section: the transaction table: uint16 count, 32 byte
// transaction hash per unique transaction of all lists.
//
// INCLUSION EVALS section: uint16 count, per evaluated target block: uint32
// record length + record:
//
//	├── BlockRoot:   32 bytes (beacon block at slot S+1)
//	├── BlockHash:   32 bytes (its execution payload)
//	├── BlockNumber: uint64
//	├── GasLimit:    uint64
//	├── GasUsed:     uint64
//	├── BaseFee:     uint64
//	├── Timestamp:   uint64
//	├── ListCount:   uint16
//	├── ListFlags:   uint8 per list, in list section order (ILListFlag*)
//	├── TxCount:     uint16
//	└── per transaction, in transaction table order:
//	    ├── Status:    uint8 (ILTxStatus*)
//	    ├── DetailLen: uint8
//	    └── Detail:    Flags uint8 (ILTxFlag*), Nonce uint64, Balance (remaining
//	                   bytes, big endian without leading zeros)
//
// INCLUSION TXS section (snappy compressed): the raw transactions in
// transaction table order: uint32 count, per transaction: uvarint length +
// bytes. It is by far the largest section and stored last, so readers that
// only need hashes and outcomes can skip it.
package types

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/golang/snappy"
)

// Inclusion list transaction statuses. The values are persisted, so existing
// ones must not change. The status tells whether and why the execution payload
// of the target block may omit the transaction.
const (
	// ILTxStatusUnknown: not evaluated, or the sender state was not available.
	ILTxStatusUnknown uint8 = 0
	// ILTxStatusIncluded: present in the target payload.
	ILTxStatusIncluded uint8 = 1
	// ILTxStatusIncludedEarlier: already included in the payload the target builds on.
	ILTxStatusIncludedEarlier uint8 = 2

	// ILTxStatusListLate: only carried by lists seen after the inclusion list due time.
	ILTxStatusListLate uint8 = 10
	// ILTxStatusListEquivocation: only carried by lists of equivocating members.
	ILTxStatusListEquivocation uint8 = 11
	// ILTxStatusListWrongBranch: only carried by lists for another committee shuffling.
	ILTxStatusListWrongBranch uint8 = 12
	// ILTxStatusFrameTx: frame transactions are exempt from the satisfaction check.
	ILTxStatusFrameTx uint8 = 13

	// ILTxStatusGasLimit: the transaction gas limit exceeds the gas left in the payload.
	ILTxStatusGasLimit uint8 = 20
	// ILTxStatusMalformed: not decodable, or the sender is not recoverable.
	ILTxStatusMalformed uint8 = 21
	// ILTxStatusFeeCapTooLow: max fee per gas below the payload's base fee.
	ILTxStatusFeeCapTooLow uint8 = 22
	// ILTxStatusNonceTooLow: the sender nonce is already consumed.
	ILTxStatusNonceTooLow uint8 = 23
	// ILTxStatusNonceTooHigh: nonce gap, the transaction is not executable yet.
	ILTxStatusNonceTooHigh uint8 = 24
	// ILTxStatusInsufficientFunds: the sender cannot cover gas * fee cap + value.
	ILTxStatusInsufficientFunds uint8 = 25
	// ILTxStatusSenderHasCode: the sender has non-delegated code (EIP-3607).
	ILTxStatusSenderHasCode uint8 = 26
	// ILTxStatusExpired: the transaction's expiry deadline has passed.
	ILTxStatusExpired uint8 = 27
	// ILTxStatusBlobTx: blob transactions cannot be appended to a payload.
	ILTxStatusBlobTx uint8 = 28

	// ILTxStatusUnsatisfied: valid against the post-state and fitting, but
	// missing from the payload: an inclusion list violation.
	ILTxStatusUnsatisfied uint8 = 40
)

// Inclusion list flags of an evaluation, one set per list.
const (
	// ILListFlagEvaluated is set for every list the evaluation covers.
	ILListFlagEvaluated uint8 = 1 << 0
	// ILListFlagLate: seen after the inclusion list due time, not enforced.
	ILListFlagLate uint8 = 1 << 1
	// ILListFlagEquivocation: the member published conflicting lists, not enforced.
	ILListFlagEquivocation uint8 = 1 << 2
	// ILListFlagWrongBranch: the dependent root does not match the target
	// block's committee shuffling, not enforced.
	ILListFlagWrongBranch uint8 = 1 << 3
)

// Inclusion list transaction detail flags.
const (
	// ILTxFlagHasState: Nonce and Balance hold the sender state at the target payload.
	ILTxFlagHasState uint8 = 1 << 0
	// ILTxFlagSenderHasCode: the sender has non-delegated code.
	ILTxFlagSenderHasCode uint8 = 1 << 1
)

// SlotInclusionLists holds the inclusion lists published in a slot and their
// evaluations against the blocks of the following slot.
type SlotInclusionLists struct {
	// Committees holds the inclusion list committee of the slot, one entry per
	// committee shuffling (dependent root).
	Committees []*SlotInclusionListCommittee
	// TxHashes is the table of unique transactions of all lists.
	TxHashes [][32]byte
	// Transactions holds the raw transactions aligned with TxHashes. Nil if
	// the raw transactions were not loaded.
	Transactions [][]byte
	Lists        []*SlotInclusionList
	Evals        []*SlotInclusionListEval
}

// SlotInclusionListCommittee is the inclusion list committee of a slot for one
// committee shuffling.
type SlotInclusionListCommittee struct {
	DependentRoot [32]byte
	// Members holds the validator indexes in committee order.
	Members []uint64
}

// SlotInclusionList is one signed inclusion list with its observations.
type SlotInclusionList struct {
	ValidatorIndex uint64
	DependentRoot  [32]byte
	Signature      [96]byte
	// TxRefs holds the list's transactions in list order as indexes into the
	// transaction table.
	TxRefs []uint16
	// SeenMask bit i is set if the client at table index i observed the list.
	SeenMask []byte
	// SeenTimes holds one first-seen offset (ms from slot start) per set mask
	// bit, in ascending client index order.
	SeenTimes []int32
}

// SlotInclusionListEval is the evaluation of a slot's inclusion lists against
// the execution payload of one block of the following slot.
type SlotInclusionListEval struct {
	BlockRoot   [32]byte
	BlockHash   [32]byte
	BlockNumber uint64
	GasLimit    uint64
	GasUsed     uint64
	BaseFee     uint64
	Timestamp   uint64
	// ListFlags holds the ILListFlag* bits per list, aligned with Lists.
	ListFlags []uint8
	// Txs holds the outcome per transaction, aligned with TxHashes.
	Txs []SlotInclusionListTx
}

// SlotInclusionListTx is the outcome of one inclusion list transaction.
type SlotInclusionListTx struct {
	Status uint8 // ILTxStatus*
	Flags  uint8 // ILTxFlag*
	// Nonce and Balance (big endian, no leading zeros) are the sender state at
	// the target payload's post-state. Valid if ILTxFlagHasState is set.
	Nonce   uint64
	Balance []byte
}

// Key returns the string key identifying this list across objects.
func (l *SlotInclusionList) Key() string {
	key := make([]byte, 8+len(l.Signature))
	binary.BigEndian.PutUint64(key, l.ValidatorIndex)
	copy(key[8:], l.Signature[:])
	return string(key)
}

// SeenByClientIndex returns a map from client table index to first-seen offset
// (ms from slot start) for all clients that observed the list.
func (l *SlotInclusionList) SeenByClientIndex() map[int]int32 {
	return seenByClientIndex(l.SeenMask, l.SeenTimes)
}

// FirstSeen returns the earliest observation offset across all clients and
// whether the list has any observation.
func (l *SlotInclusionList) FirstSeen() (int32, bool) {
	if len(l.SeenTimes) == 0 {
		return 0, false
	}

	first := l.SeenTimes[0]
	for _, t := range l.SeenTimes[1:] {
		if t < first {
			first = t
		}
	}

	return first, true
}

// GetEval returns the evaluation against the block with the given root, or nil.
func (s *SlotInclusionLists) GetEval(blockRoot [32]byte) *SlotInclusionListEval {
	if s == nil {
		return nil
	}
	for _, eval := range s.Evals {
		if eval.BlockRoot == blockRoot {
			return eval
		}
	}
	return nil
}

// GetCommittee returns the committee for the given dependent root, falling
// back to the first stored committee. Returns nil if none is stored.
func (s *SlotInclusionLists) GetCommittee(dependentRoot [32]byte) *SlotInclusionListCommittee {
	if s == nil || len(s.Committees) == 0 {
		return nil
	}
	for _, committee := range s.Committees {
		if committee.DependentRoot == dependentRoot {
			return committee
		}
	}
	return s.Committees[0]
}

// CountUnsatisfied returns the number of unsatisfied transactions and whether
// the evaluation is complete (no transaction left with an unknown status).
func (e *SlotInclusionListEval) CountUnsatisfied() (unsatisfied int, complete bool) {
	complete = true
	for i := range e.Txs {
		switch e.Txs[i].Status {
		case ILTxStatusUnsatisfied:
			unsatisfied++
		case ILTxStatusUnknown:
			complete = false
		}
	}
	return unsatisfied, complete
}

// encodeInclusionListSections encodes the inclusion list sections of an
// object. The raw transaction section is returned separately as it is stored
// after all other sections.
func encodeInclusionListSections(s *SlotInclusionLists, clientCount int) ([]*SlotMetaSection, *SlotMetaSection, error) {
	if len(s.TxHashes) > 0xffff {
		return nil, nil, fmt.Errorf("too many inclusion list transactions: %d", len(s.TxHashes))
	}
	if len(s.Transactions) != len(s.TxHashes) {
		return nil, nil, fmt.Errorf("inclusion list transactions not loaded: %d != %d", len(s.Transactions), len(s.TxHashes))
	}
	if len(s.Lists) > 0xffff {
		return nil, nil, fmt.Errorf("too many inclusion lists: %d", len(s.Lists))
	}
	if len(s.Evals) > 0xffff {
		return nil, nil, fmt.Errorf("too many inclusion list evaluations: %d", len(s.Evals))
	}
	if len(s.Committees) > 0xff {
		return nil, nil, fmt.Errorf("too many inclusion list committees: %d", len(s.Committees))
	}

	var committeeBuf bytes.Buffer
	committeeBuf.WriteByte(uint8(len(s.Committees)))
	for _, committee := range s.Committees {
		if len(committee.Members) > 0xffff {
			return nil, nil, fmt.Errorf("inclusion list committee too large: %d", len(committee.Members))
		}
		committeeBuf.Write(committee.DependentRoot[:])
		_ = binary.Write(&committeeBuf, binary.BigEndian, uint16(len(committee.Members)))
		for _, member := range committee.Members {
			_ = binary.Write(&committeeBuf, binary.BigEndian, member)
		}
	}

	var hashBuf, txBuf bytes.Buffer
	var varint [binary.MaxVarintLen64]byte
	_ = binary.Write(&hashBuf, binary.BigEndian, uint16(len(s.TxHashes)))
	_ = binary.Write(&txBuf, binary.BigEndian, uint32(len(s.Transactions)))
	for i, tx := range s.Transactions {
		hashBuf.Write(s.TxHashes[i][:])
		txBuf.Write(varint[:binary.PutUvarint(varint[:], uint64(len(tx)))])
		txBuf.Write(tx)
	}

	maskLen := (clientCount + 7) / 8
	var listBuf, record bytes.Buffer
	_ = binary.Write(&listBuf, binary.BigEndian, uint16(len(s.Lists)))
	for _, list := range s.Lists {
		if len(list.TxRefs) > 0xffff {
			return nil, nil, fmt.Errorf("too many inclusion list transaction refs: %d", len(list.TxRefs))
		}

		record.Reset()
		_ = binary.Write(&record, binary.BigEndian, list.ValidatorIndex)
		record.Write(list.DependentRoot[:])
		record.Write(list.Signature[:])
		_ = binary.Write(&record, binary.BigEndian, uint16(len(list.TxRefs)))
		for _, ref := range list.TxRefs {
			if int(ref) >= len(s.TxHashes) {
				return nil, nil, fmt.Errorf("inclusion list transaction ref out of range: %d", ref)
			}
			_ = binary.Write(&record, binary.BigEndian, ref)
		}
		if err := appendSeen(&record, list.SeenMask, list.SeenTimes, maskLen); err != nil {
			return nil, nil, fmt.Errorf("inclusion list: %w", err)
		}

		_ = binary.Write(&listBuf, binary.BigEndian, uint32(record.Len()))
		listBuf.Write(record.Bytes())
	}

	var evalBuf bytes.Buffer
	_ = binary.Write(&evalBuf, binary.BigEndian, uint16(len(s.Evals)))
	for _, eval := range s.Evals {
		record.Reset()
		record.Write(eval.BlockRoot[:])
		record.Write(eval.BlockHash[:])
		_ = binary.Write(&record, binary.BigEndian, eval.BlockNumber)
		_ = binary.Write(&record, binary.BigEndian, eval.GasLimit)
		_ = binary.Write(&record, binary.BigEndian, eval.GasUsed)
		_ = binary.Write(&record, binary.BigEndian, eval.BaseFee)
		_ = binary.Write(&record, binary.BigEndian, eval.Timestamp)

		// Flags and outcomes are aligned with the list and transaction tables;
		// missing entries are written as not evaluated.
		_ = binary.Write(&record, binary.BigEndian, uint16(len(s.Lists)))
		for i := range s.Lists {
			var flags uint8
			if i < len(eval.ListFlags) {
				flags = eval.ListFlags[i]
			}
			record.WriteByte(flags)
		}

		_ = binary.Write(&record, binary.BigEndian, uint16(len(s.TxHashes)))
		for i := range s.TxHashes {
			var tx SlotInclusionListTx
			if i < len(eval.Txs) {
				tx = eval.Txs[i]
			}
			record.WriteByte(tx.Status)

			if tx.Flags == 0 {
				record.WriteByte(0)
				continue
			}

			balance := bytes.TrimLeft(tx.Balance, "\x00")
			if len(balance) > 32 {
				return nil, nil, fmt.Errorf("inclusion list sender balance too large: %d bytes", len(balance))
			}
			record.WriteByte(uint8(1 + 8 + len(balance)))
			record.WriteByte(tx.Flags)
			_ = binary.Write(&record, binary.BigEndian, tx.Nonce)
			record.Write(balance)
		}

		_ = binary.Write(&evalBuf, binary.BigEndian, uint32(record.Len()))
		evalBuf.Write(record.Bytes())
	}

	sections := []*SlotMetaSection{
		{Type: MetaSectionInclusionCommittee, Data: committeeBuf.Bytes()},
		{Type: MetaSectionInclusionLists, Data: listBuf.Bytes()},
		{Type: MetaSectionInclusionTxHashes, Data: hashBuf.Bytes()},
		{Type: MetaSectionInclusionEvals, Data: evalBuf.Bytes()},
	}
	txSection := &SlotMetaSection{
		Type:  MetaSectionInclusionTxs,
		Flags: MetaSectionFlagSnappy,
		Data:  snappy.Encode(nil, txBuf.Bytes()),
	}

	return sections, txSection, nil
}

// decodeInclusionListSections decodes the given inclusion list sections of an
// object.
func decodeInclusionListSections(sections []*SlotMetaSection, maskLen int) (*SlotInclusionLists, error) {
	s := &SlotInclusionLists{
		Committees: []*SlotInclusionListCommittee{},
		TxHashes:   [][32]byte{},
		Lists:      []*SlotInclusionList{},
		Evals:      []*SlotInclusionListEval{},
	}

	hasHashes := false
	for _, section := range sections {
		payload, err := section.payload()
		if err != nil {
			return nil, err
		}

		switch section.Type {
		case MetaSectionInclusionCommittee:
			s.Committees, err = decodeInclusionCommittees(payload)
		case MetaSectionInclusionTxHashes:
			s.TxHashes, err = decodeInclusionTxHashes(payload)
			hasHashes = true
		case MetaSectionInclusionTxs:
			s.Transactions, err = decodeInclusionTxs(payload)
		case MetaSectionInclusionLists:
			s.Lists, err = decodeInclusionLists(payload, maskLen)
		case MetaSectionInclusionEvals:
			s.Evals, err = decodeInclusionEvals(payload)
		}
		if err != nil {
			return nil, err
		}
	}

	if s.Transactions != nil && hasHashes && len(s.Transactions) != len(s.TxHashes) {
		return nil, fmt.Errorf("inclusion list transaction count mismatch: %d != %d", len(s.Transactions), len(s.TxHashes))
	}
	if hasHashes {
		for _, list := range s.Lists {
			for _, ref := range list.TxRefs {
				if int(ref) >= len(s.TxHashes) {
					return nil, fmt.Errorf("inclusion list transaction ref out of range: %d", ref)
				}
			}
		}
	}

	return s, nil
}

// decodeInclusionCommittees decodes the committee section.
func decodeInclusionCommittees(data []byte) ([]*SlotInclusionListCommittee, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("truncated inclusion committee section")
	}
	count := int(data[0])
	pos := 1

	committees := make([]*SlotInclusionListCommittee, 0, count)
	for range count {
		if pos+34 > len(data) {
			return nil, fmt.Errorf("truncated inclusion committee")
		}
		committee := &SlotInclusionListCommittee{}
		copy(committee.DependentRoot[:], data[pos:pos+32])
		memberCount := int(binary.BigEndian.Uint16(data[pos+32 : pos+34]))
		pos += 34

		if pos+8*memberCount > len(data) {
			return nil, fmt.Errorf("truncated inclusion committee members")
		}
		committee.Members = make([]uint64, 0, memberCount)
		for range memberCount {
			committee.Members = append(committee.Members, binary.BigEndian.Uint64(data[pos:pos+8]))
			pos += 8
		}

		committees = append(committees, committee)
	}

	return committees, nil
}

// decodeInclusionTxHashes decodes the transaction hash table.
func decodeInclusionTxHashes(data []byte) ([][32]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("truncated inclusion tx hashes section")
	}
	count := int(binary.BigEndian.Uint16(data[0:2]))
	if 2+32*count > len(data) {
		return nil, fmt.Errorf("truncated inclusion tx hashes")
	}

	hashes := make([][32]byte, count)
	for i := range count {
		copy(hashes[i][:], data[2+32*i:])
	}

	return hashes, nil
}

// decodeInclusionTxs decodes the raw transaction table.
func decodeInclusionTxs(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("truncated inclusion txs section")
	}
	count := int(binary.BigEndian.Uint32(data[0:4]))
	pos := 4

	txs := make([][]byte, 0, min(count, 0xffff))
	for range count {
		txLen, n := binary.Uvarint(data[pos:])
		if n <= 0 || uint64(len(data)-pos-n) < txLen {
			return nil, fmt.Errorf("truncated inclusion list transaction")
		}
		pos += n
		txs = append(txs, bytes.Clone(data[pos:pos+int(txLen)]))
		pos += int(txLen)
	}

	return txs, nil
}

// readRecord reads a uint32 length-prefixed record from data at pos. Returns
// the record and the new position.
func readRecord(data []byte, pos int) ([]byte, int, error) {
	if pos+4 > len(data) {
		return nil, pos, fmt.Errorf("truncated record length")
	}
	recordLen := int(binary.BigEndian.Uint32(data[pos : pos+4]))
	pos += 4
	if recordLen > len(data)-pos {
		return nil, pos, fmt.Errorf("truncated record")
	}
	return data[pos : pos+recordLen], pos + recordLen, nil
}

// decodeInclusionLists decodes the inclusion list records.
func decodeInclusionLists(data []byte, maskLen int) ([]*SlotInclusionList, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("truncated inclusion lists section")
	}
	count := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2

	const fixedSize = 8 + 32 + 96 + 2

	lists := make([]*SlotInclusionList, 0, count)
	for range count {
		record, newPos, err := readRecord(data, pos)
		if err != nil {
			return nil, fmt.Errorf("inclusion list: %w", err)
		}
		pos = newPos

		if len(record) < fixedSize {
			return nil, fmt.Errorf("truncated inclusion list record")
		}
		list := &SlotInclusionList{
			ValidatorIndex: binary.BigEndian.Uint64(record[0:8]),
		}
		copy(list.DependentRoot[:], record[8:40])
		copy(list.Signature[:], record[40:136])

		txCount := int(binary.BigEndian.Uint16(record[136:138]))
		if fixedSize+2*txCount > len(record) {
			return nil, fmt.Errorf("truncated inclusion list transaction refs")
		}
		list.TxRefs = make([]uint16, 0, txCount)
		for i := range txCount {
			list.TxRefs = append(list.TxRefs, binary.BigEndian.Uint16(record[fixedSize+2*i:]))
		}

		mask, times, _, err := readSeen(record, fixedSize+2*txCount, maskLen)
		if err != nil {
			return nil, fmt.Errorf("inclusion list: %w", err)
		}
		list.SeenMask = mask
		list.SeenTimes = times

		lists = append(lists, list)
	}

	return lists, nil
}

// decodeInclusionEvals decodes the inclusion list evaluation records.
func decodeInclusionEvals(data []byte) ([]*SlotInclusionListEval, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("truncated inclusion evals section")
	}
	count := int(binary.BigEndian.Uint16(data[0:2]))
	pos := 2

	const fixedSize = 32 + 32 + 5*8

	evals := make([]*SlotInclusionListEval, 0, count)
	for range count {
		record, newPos, err := readRecord(data, pos)
		if err != nil {
			return nil, fmt.Errorf("inclusion list evaluation: %w", err)
		}
		pos = newPos

		if len(record) < fixedSize+2 {
			return nil, fmt.Errorf("truncated inclusion list evaluation record")
		}
		eval := &SlotInclusionListEval{
			BlockNumber: binary.BigEndian.Uint64(record[64:72]),
			GasLimit:    binary.BigEndian.Uint64(record[72:80]),
			GasUsed:     binary.BigEndian.Uint64(record[80:88]),
			BaseFee:     binary.BigEndian.Uint64(record[88:96]),
			Timestamp:   binary.BigEndian.Uint64(record[96:104]),
		}
		copy(eval.BlockRoot[:], record[0:32])
		copy(eval.BlockHash[:], record[32:64])

		rpos := fixedSize
		listCount := int(binary.BigEndian.Uint16(record[rpos:]))
		rpos += 2
		if rpos+listCount+2 > len(record) {
			return nil, fmt.Errorf("truncated inclusion list evaluation flags")
		}
		eval.ListFlags = bytes.Clone(record[rpos : rpos+listCount])
		rpos += listCount

		txCount := int(binary.BigEndian.Uint16(record[rpos:]))
		rpos += 2
		eval.Txs = make([]SlotInclusionListTx, txCount)
		for i := range txCount {
			if rpos+2 > len(record) {
				return nil, fmt.Errorf("truncated inclusion list transaction outcome")
			}
			eval.Txs[i].Status = record[rpos]
			detailLen := int(record[rpos+1])
			rpos += 2
			if rpos+detailLen > len(record) {
				return nil, fmt.Errorf("truncated inclusion list transaction detail")
			}
			if detailLen >= 9 {
				eval.Txs[i].Flags = record[rpos]
				eval.Txs[i].Nonce = binary.BigEndian.Uint64(record[rpos+1 : rpos+9])
				eval.Txs[i].Balance = bytes.Clone(record[rpos+9 : rpos+detailLen])
			}
			rpos += detailLen
		}

		evals = append(evals, eval)
	}

	return evals, nil
}

// mergeSlotInclusionLists merges the inclusion lists of two objects for the
// same slot. Transactions are unified by hash and lists by validator and
// signature, keeping the earliest observation per client. Evaluations are
// unified by target block; for a block present in both, the outcomes of the
// second (newer) object win unless they are unknown. Committees are unified by
// dependent root, the newer one winning. clientIdx maps client names to
// indexes of the merged client table of size clientCount.
func mergeSlotInclusionLists(
	a *SlotInclusionLists, aClients []string,
	b *SlotInclusionLists, bClients []string,
	clientIdx map[string]int, clientCount int,
) *SlotInclusionLists {
	if a == nil && b == nil {
		return nil
	}

	type source struct {
		lists   *SlotInclusionLists
		clients []string
		txMap   []uint16 // source transaction index -> merged index
		listMap []int    // source list index -> merged index
	}
	sources := make([]*source, 0, 2)
	if a != nil {
		sources = append(sources, &source{lists: a, clients: aClients})
	}
	if b != nil {
		sources = append(sources, &source{lists: b, clients: bClients})
	}

	merged := &SlotInclusionLists{
		Committees: []*SlotInclusionListCommittee{},
		TxHashes:   [][32]byte{},
		Lists:      []*SlotInclusionList{},
		Evals:      []*SlotInclusionListEval{},
	}

	committeeIdx := make(map[[32]byte]int, 2)
	txIdx := make(map[[32]byte]uint16, 64)
	txData := make([][]byte, 0, 64)
	listIdx := make(map[string]int, 16)
	listSeen := make([]map[int]int32, 0, 16)

	for _, src := range sources {
		for _, committee := range src.lists.Committees {
			if idx, ok := committeeIdx[committee.DependentRoot]; ok {
				merged.Committees[idx] = committee
				continue
			}
			committeeIdx[committee.DependentRoot] = len(merged.Committees)
			merged.Committees = append(merged.Committees, committee)
		}

		src.txMap = make([]uint16, len(src.lists.TxHashes))
		for i, hash := range src.lists.TxHashes {
			idx, ok := txIdx[hash]
			if !ok {
				idx = uint16(len(merged.TxHashes))
				txIdx[hash] = idx
				merged.TxHashes = append(merged.TxHashes, hash)
				txData = append(txData, nil)
			}
			if txData[idx] == nil && i < len(src.lists.Transactions) {
				txData[idx] = src.lists.Transactions[i]
			}
			src.txMap[i] = idx
		}

		src.listMap = make([]int, len(src.lists.Lists))
		for i, list := range src.lists.Lists {
			key := list.Key()
			idx, ok := listIdx[key]
			if !ok {
				idx = len(merged.Lists)
				listIdx[key] = idx

				refs := make([]uint16, 0, len(list.TxRefs))
				for _, ref := range list.TxRefs {
					if int(ref) < len(src.txMap) {
						refs = append(refs, src.txMap[ref])
					}
				}
				merged.Lists = append(merged.Lists, &SlotInclusionList{
					ValidatorIndex: list.ValidatorIndex,
					DependentRoot:  list.DependentRoot,
					Signature:      list.Signature,
					TxRefs:         refs,
				})
				listSeen = append(listSeen, make(map[int]int32, 8))
			}
			src.listMap[i] = idx

			mergeSeen(listSeen[idx], list.SeenByClientIndex(), src.clients, clientIdx)
		}
	}

	// The raw transactions are only kept if they are known for the whole table.
	merged.Transactions = txData
	for _, tx := range txData {
		if tx == nil {
			merged.Transactions = nil
			break
		}
	}

	for i, list := range merged.Lists {
		list.SeenMask, list.SeenTimes = NewSeenObservations(listSeen[i], clientCount)
	}

	evalIdx := make(map[[32]byte]*SlotInclusionListEval, 2)
	for _, src := range sources {
		for _, srcEval := range src.lists.Evals {
			eval := evalIdx[srcEval.BlockRoot]
			if eval == nil {
				eval = &SlotInclusionListEval{
					ListFlags: make([]uint8, len(merged.Lists)),
					Txs:       make([]SlotInclusionListTx, len(merged.TxHashes)),
				}
				evalIdx[srcEval.BlockRoot] = eval
				merged.Evals = append(merged.Evals, eval)
			}

			eval.BlockRoot = srcEval.BlockRoot
			eval.BlockHash = srcEval.BlockHash
			eval.BlockNumber = srcEval.BlockNumber
			eval.GasLimit = srcEval.GasLimit
			eval.GasUsed = srcEval.GasUsed
			eval.BaseFee = srcEval.BaseFee
			eval.Timestamp = srcEval.Timestamp

			for i, flags := range srcEval.ListFlags {
				if i >= len(src.listMap) {
					break
				}
				if flags&ILListFlagEvaluated != 0 || eval.ListFlags[src.listMap[i]] == 0 {
					eval.ListFlags[src.listMap[i]] = flags
				}
			}
			for i := range srcEval.Txs {
				if i >= len(src.txMap) {
					break
				}
				dst := &eval.Txs[src.txMap[i]]
				if srcEval.Txs[i].Status != ILTxStatusUnknown || dst.Status == ILTxStatusUnknown {
					*dst = srcEval.Txs[i]
				}
			}
		}
	}

	return merged
}
