package db

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/dbtypes"
)

// The filtered list queries fetch the page and the total count separately. These tests run
// every filter branch of each query on the configured engine and check that the count matches
// the full result set, that consecutive pages concatenate back to it in descending order, and
// that a page past the end still reports the real total.

const (
	filteredQueryRows      = 30
	filteredQueryTxRows    = 25
	filteredQueryFinalized = 115
)

type filteredQuerySeed struct {
	withdrawals []*dbtypes.Withdrawal
	deposits    []*dbtypes.Deposit
	depositTxs  []*dbtypes.DepositTx
	mevBlocks   []*dbtypes.MevBlock
}

func testPubkey(i int) []byte {
	pubkey := make([]byte, 48)
	pubkey[0] = byte(i + 1)
	return pubkey
}

func testAddress(i int) []byte {
	address := make([]byte, 20)
	address[0] = byte(i + 1)
	return address
}

func testCreds(credType byte, address []byte) []byte {
	creds := make([]byte, 32)
	creds[0] = credType
	copy(creds[12:], address)
	return creds
}

func seedFilteredQueries(t *testing.T) *filteredQuerySeed {
	t.Helper()
	seed := &filteredQuerySeed{}

	for i := range filteredQueryRows {
		slot := uint64(100 + i)
		seed.withdrawals = append(seed.withdrawals, &dbtypes.Withdrawal{
			BlockUid:  slot<<16 | uint64(i%2),
			BlockIdx:  int16(i % 2),
			Type:      uint8(1 + i%3),
			Orphaned:  i%10 == 9,
			ForkId:    map[bool]uint64{false: 1, true: 2}[i%10 == 9],
			Validator: uint64(i % 3),
			AccountID: map[bool]uint64{false: 8, true: 7}[i%5 == 0],
			Amount:    uint64(1000 * (i + 1)),
		})

		creds := make([]byte, 32)
		switch i % 4 {
		case 0:
			creds = testCreds(0x01, testAddress(0))
		case 1:
			creds = testCreds(0x02, testAddress(0))
		}
		depositIndex := uint64(i)
		seed.deposits = append(seed.deposits, &dbtypes.Deposit{
			Index:                 &depositIndex,
			SlotNumber:            uint64(200 + i),
			SlotRoot:              []byte{byte(i)},
			Orphaned:              i%6 == 5,
			PublicKey:             testPubkey(i % 3),
			WithdrawalCredentials: creds,
			Amount:                uint64(i+1) * 1000000000,
			ForkId:                map[bool]uint64{false: 1, true: 2}[i%6 == 5],
			CredType:              creds[0],
		})

		if i < filteredQueryTxRows {
			validSignature := uint8(1)
			switch {
			case i%7 == 0:
				validSignature = 0
			case i == 3:
				validSignature = 2
			}
			seed.depositTxs = append(seed.depositTxs, &dbtypes.DepositTx{
				Index:                 uint64(i),
				BlockNumber:           uint64(10 + i),
				BlockTime:             uint64(1000 + i),
				BlockRoot:             []byte{byte(i)},
				PublicKey:             testPubkey(i % 3),
				WithdrawalCredentials: creds,
				Amount:                uint64(i+1) * 1000000000,
				Signature:             []byte{},
				ValidSignature:        validSignature,
				Orphaned:              i%8 == 7,
				TxHash:                []byte{byte(i)},
				TxSender:              testAddress(1 + i%2),
				TxTarget:              testAddress(3),
				ForkId:                map[bool]uint64{false: 1, true: 2}[i%8 == 7],
			})
		}

		seed.mevBlocks = append(seed.mevBlocks, &dbtypes.MevBlock{
			SlotNumber:    uint64(300 + i),
			BlockHash:     []byte{byte(i)},
			BuilderPubkey: testPubkey(10 + i%2),
			ProposerIndex: uint64(i % 3),
			Proposed:      uint8(i % 3),
			SeenbyRelays:  uint64(1) << (i % 4),
			FeeRecipient:  []byte{},
			BlockValue:    []byte{},
		})
	}

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		for i := range 3 {
			if err := InsertValidator(context.Background(), tx, &dbtypes.Validator{
				ValidatorIndex: uint64(i), Pubkey: testPubkey(i), WithdrawalCredentials: make([]byte, 32),
			}); err != nil {
				return err
			}
		}
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{
			{Index: 0, Name: "alpha-node"},
			{Index: 1, Name: "beta-node"},
		}); err != nil {
			return err
		}
		if err := InsertWithdrawals(context.Background(), tx, seed.withdrawals); err != nil {
			return err
		}
		if err := InsertDeposits(context.Background(), tx, seed.deposits); err != nil {
			return err
		}
		if err := InsertDepositTxs(context.Background(), tx, seed.depositTxs); err != nil {
			return err
		}
		return InsertMevBlocks(context.Background(), tx, seed.mevBlocks)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	return seed
}

// pagedQuery runs one filtered query and returns the page as descending row ranks plus the
// reported total.
type pagedQuery func(offset uint64, limit uint32) ([]uint64, uint64, error)

func checkPagedQuery(t *testing.T, query pagedQuery, want []uint64) {
	t.Helper()

	all, count, err := query(0, 1000)
	if err != nil {
		t.Fatalf("full fetch: %v", err)
	}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("full fetch returned %v, want %v", all, want)
	}
	if count != uint64(len(want)) {
		t.Fatalf("full fetch count %v, want %v", count, len(want))
	}
	for i := 1; i < len(all); i++ {
		if all[i] >= all[i-1] {
			t.Fatalf("rows not in descending order: %v", all)
		}
	}

	const pageSize = 7
	paged := []uint64{}
	for offset := uint64(0); ; offset += pageSize {
		page, count, err := query(offset, pageSize)
		if err != nil {
			t.Fatalf("page at %v: %v", offset, err)
		}
		if count != uint64(len(want)) {
			t.Fatalf("page at %v count %v, want %v", offset, count, len(want))
		}
		paged = append(paged, page...)
		if len(page) < pageSize {
			break
		}
	}
	if !reflect.DeepEqual(paged, all) {
		t.Fatalf("pages concatenate to %v, want %v", paged, all)
	}

	page, count, err := query(uint64(len(want))+5, pageSize)
	if err != nil {
		t.Fatalf("page past the end: %v", err)
	}
	if len(page) != 0 || count != uint64(len(want)) {
		t.Fatalf("page past the end returned %v rows and count %v, want 0 rows and count %v", len(page), count, len(want))
	}
}

func u64p(v uint64) *uint64 {
	return new(v)
}

func TestGetWithdrawalsFilteredPaging(t *testing.T) {
	newTestDB(t)
	seed := seedFilteredQueries(t)

	expect := func(match func(w *dbtypes.Withdrawal) bool) []uint64 {
		ranks := []uint64{}
		for _, w := range slices.Backward(seed.withdrawals) {
			if match(w) {
				ranks = append(ranks, w.BlockUid)
			}
		}
		return ranks
	}
	slot := func(w *dbtypes.Withdrawal) uint64 { return w.BlockUid >> 16 }

	cases := []struct {
		name   string
		filter *dbtypes.WithdrawalFilter
		match  func(w *dbtypes.Withdrawal) bool
	}{
		{"all", &dbtypes.WithdrawalFilter{WithOrphaned: 1}, func(w *dbtypes.Withdrawal) bool { return true }},
		{"canonical", &dbtypes.WithdrawalFilter{WithOrphaned: 0}, func(w *dbtypes.Withdrawal) bool {
			return slot(w) > filteredQueryFinalized || !w.Orphaned
		}},
		{"orphaned", &dbtypes.WithdrawalFilter{WithOrphaned: 2}, func(w *dbtypes.Withdrawal) bool {
			return slot(w) > filteredQueryFinalized || w.Orphaned
		}},
		{"validator range", &dbtypes.WithdrawalFilter{WithOrphaned: 1, MinIndex: 1, MaxIndex: 1}, func(w *dbtypes.Withdrawal) bool {
			return w.Validator == 1
		}},
		{"account", &dbtypes.WithdrawalFilter{WithOrphaned: 1, AccountID: u64p(7)}, func(w *dbtypes.Withdrawal) bool {
			return w.AccountID == 7
		}},
		{"types", &dbtypes.WithdrawalFilter{WithOrphaned: 1, Types: []uint8{1, 3}}, func(w *dbtypes.Withdrawal) bool {
			return w.Type == 1 || w.Type == 3
		}},
		{"amount range", &dbtypes.WithdrawalFilter{WithOrphaned: 1, MinAmount: u64p(5000), MaxAmount: u64p(20000)}, func(w *dbtypes.Withdrawal) bool {
			return w.Amount >= 5000 && w.Amount <= 20000
		}},
		{"slot range", &dbtypes.WithdrawalFilter{WithOrphaned: 1, MinSlot: u64p(110), MaxSlot: u64p(120)}, func(w *dbtypes.Withdrawal) bool {
			return slot(w) >= 110 && slot(w) <= 120
		}},
		{"slot range clamped", &dbtypes.WithdrawalFilter{WithOrphaned: 1, MinSlot: u64p(125), MaxSlot: u64p(1 << 60)}, func(w *dbtypes.Withdrawal) bool {
			return slot(w) >= 125
		}},
		{"validator name", &dbtypes.WithdrawalFilter{WithOrphaned: 1, ValidatorName: "alpha"}, func(w *dbtypes.Withdrawal) bool {
			return w.Validator == 0
		}},
		{"combined", &dbtypes.WithdrawalFilter{WithOrphaned: 0, Types: []uint8{2}, MinSlot: u64p(105)}, func(w *dbtypes.Withdrawal) bool {
			return w.Type == 2 && slot(w) >= 105 && (slot(w) > filteredQueryFinalized || !w.Orphaned)
		}},
		{"no match", &dbtypes.WithdrawalFilter{WithOrphaned: 1, AccountID: u64p(99)}, func(w *dbtypes.Withdrawal) bool { return false }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkPagedQuery(t, func(offset uint64, limit uint32) ([]uint64, uint64, error) {
				rows, count, err := GetWithdrawalsFiltered(context.Background(), offset, limit, filteredQueryFinalized, c.filter)
				ranks := make([]uint64, len(rows))
				for i, row := range rows {
					ranks[i] = row.BlockUid
				}
				return ranks, count, err
			}, expect(c.match))
		})
	}
}

func TestGetDepositsFilteredPaging(t *testing.T) {
	newTestDB(t)
	seed := seedFilteredQueries(t)

	txs := map[uint64]*dbtypes.DepositTx{}
	for _, tx := range seed.depositTxs {
		txs[tx.Index] = tx
	}
	expect := func(match func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool) []uint64 {
		ranks := []uint64{}
		for _, d := range slices.Backward(seed.deposits) {
			if match(d, txs[*d.Index]) {
				ranks = append(ranks, *d.Index)
			}
		}
		return ranks
	}
	ethAmount := func(d *dbtypes.Deposit) uint64 { return d.Amount / 1000000000 }

	cases := []struct {
		name     string
		filter   *dbtypes.DepositFilter
		txFilter *dbtypes.DepositTxFilter
		match    func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool
	}{
		{"all", &dbtypes.DepositFilter{WithOrphaned: 1}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool { return true }},
		{"canonical", &dbtypes.DepositFilter{WithOrphaned: 0}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool { return d.ForkId == 1 }},
		{"orphaned", &dbtypes.DepositFilter{WithOrphaned: 2}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool { return d.ForkId != 1 }},
		{"index range", &dbtypes.DepositFilter{WithOrphaned: 1, MinIndex: 5, MaxIndex: 15}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return *d.Index >= 5 && *d.Index <= 15
		}},
		{"pubkey", &dbtypes.DepositFilter{WithOrphaned: 1, PublicKey: testPubkey(1)}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return *d.Index%3 == 1
		}},
		{"amount range", &dbtypes.DepositFilter{WithOrphaned: 1, MinAmount: 10, MaxAmount: 20}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return ethAmount(d) >= 10 && ethAmount(d) <= 20
		}},
		{"validator name", &dbtypes.DepositFilter{WithOrphaned: 1, ValidatorName: "beta"}, nil, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return *d.Index%3 == 1
		}},
		{"with txs", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 1}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return true
		}},
		{"valid only", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 0}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return tx != nil && tx.ValidSignature != 0
		}},
		{"invalid only", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 2}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return tx != nil && tx.ValidSignature == 0
		}},
		{"withdrawal address", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 1, WithdrawalAddress: testAddress(0)}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return d.CredType == 1 || d.CredType == 2
		}},
		{"cred types", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 1, WithdrawalCredTypes: []uint8{2}}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return d.CredType == 2
		}},
		{"sender", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 1, Address: testAddress(1)}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return tx != nil && tx.Index%2 == 0
		}},
		{"target", &dbtypes.DepositFilter{WithOrphaned: 1}, &dbtypes.DepositTxFilter{WithValid: 1, TargetAddress: testAddress(3)}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return tx != nil
		}},
		{"combined", &dbtypes.DepositFilter{WithOrphaned: 0, MinAmount: 3}, &dbtypes.DepositTxFilter{WithValid: 0, Address: testAddress(2)}, func(d *dbtypes.Deposit, tx *dbtypes.DepositTx) bool {
			return d.ForkId == 1 && ethAmount(d) >= 3 && tx != nil && tx.ValidSignature != 0 && tx.Index%2 == 1
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkPagedQuery(t, func(offset uint64, limit uint32) ([]uint64, uint64, error) {
				rows, count, err := GetDepositsFiltered(context.Background(), offset, limit, []uint64{1}, c.filter, c.txFilter)
				ranks := make([]uint64, len(rows))
				for i, row := range rows {
					ranks[i] = *row.Index
					if c.txFilter != nil && txs[*row.Index] != nil && (row.BlockNumber == nil || *row.BlockNumber != txs[*row.Index].BlockNumber) {
						return nil, 0, fmt.Errorf("deposit %v joined tx block %v, want %v", *row.Index, row.BlockNumber, txs[*row.Index].BlockNumber)
					}
				}
				return ranks, count, err
			}, expect(c.match))
		})
	}
}

func TestGetDepositTxsFilteredPaging(t *testing.T) {
	newTestDB(t)
	seed := seedFilteredQueries(t)

	expect := func(match func(tx *dbtypes.DepositTx) bool) []uint64 {
		ranks := []uint64{}
		for _, tx := range slices.Backward(seed.depositTxs) {
			if match(tx) {
				ranks = append(ranks, tx.Index)
			}
		}
		return ranks
	}
	ethAmount := func(tx *dbtypes.DepositTx) uint64 { return tx.Amount / 1000000000 }

	cases := []struct {
		name   string
		filter *dbtypes.DepositTxFilter
		match  func(tx *dbtypes.DepositTx) bool
	}{
		{"all", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1}, func(tx *dbtypes.DepositTx) bool { return true }},
		{"canonical", &dbtypes.DepositTxFilter{WithOrphaned: 0, WithValid: 1}, func(tx *dbtypes.DepositTx) bool { return tx.ForkId == 1 }},
		{"orphaned", &dbtypes.DepositTxFilter{WithOrphaned: 2, WithValid: 1}, func(tx *dbtypes.DepositTx) bool { return tx.ForkId != 1 }},
		{"valid only", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 0}, func(tx *dbtypes.DepositTx) bool { return tx.ValidSignature != 0 }},
		{"invalid only", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 2}, func(tx *dbtypes.DepositTx) bool { return tx.ValidSignature == 0 }},
		{"index range", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, MinIndex: 4, MaxIndex: 12}, func(tx *dbtypes.DepositTx) bool {
			return tx.Index >= 4 && tx.Index <= 12
		}},
		{"sender", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, Address: testAddress(2)}, func(tx *dbtypes.DepositTx) bool { return tx.Index%2 == 1 }},
		{"target", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, TargetAddress: testAddress(3)}, func(tx *dbtypes.DepositTx) bool { return true }},
		{"pubkey", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, PublicKey: testPubkey(2)}, func(tx *dbtypes.DepositTx) bool { return tx.Index%3 == 2 }},
		{"pubkeys", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, PublicKeys: [][]byte{testPubkey(0), testPubkey(1)}}, func(tx *dbtypes.DepositTx) bool {
			return tx.Index%3 != 2
		}},
		{"withdrawal address", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, WithdrawalAddress: testAddress(0)}, func(tx *dbtypes.DepositTx) bool {
			return tx.WithdrawalCredentials[0] == 1 || tx.WithdrawalCredentials[0] == 2
		}},
		{"amount range", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, MinAmount: 10, MaxAmount: 20}, func(tx *dbtypes.DepositTx) bool {
			return ethAmount(tx) >= 10 && ethAmount(tx) <= 20
		}},
		{"combined", &dbtypes.DepositTxFilter{WithOrphaned: 0, WithValid: 0, Address: testAddress(1), MinIndex: 2}, func(tx *dbtypes.DepositTx) bool {
			return tx.ForkId == 1 && tx.ValidSignature != 0 && tx.Index%2 == 0 && tx.Index >= 2
		}},
		{"no match", &dbtypes.DepositTxFilter{WithOrphaned: 1, WithValid: 1, PublicKey: testPubkey(9)}, func(tx *dbtypes.DepositTx) bool { return false }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkPagedQuery(t, func(offset uint64, limit uint32) ([]uint64, uint64, error) {
				rows, count, err := GetDepositTxsFiltered(context.Background(), offset, limit, []uint64{1}, c.filter)
				ranks := make([]uint64, len(rows))
				for i, row := range rows {
					ranks[i] = row.Index
				}
				return ranks, count, err
			}, expect(c.match))
		})
	}
}

func TestGetMevBlocksFilteredPaging(t *testing.T) {
	newTestDB(t)
	seed := seedFilteredQueries(t)

	expect := func(match func(b *dbtypes.MevBlock) bool) []uint64 {
		ranks := []uint64{}
		for _, b := range slices.Backward(seed.mevBlocks) {
			if match(b) {
				ranks = append(ranks, b.SlotNumber)
			}
		}
		return ranks
	}

	cases := []struct {
		name   string
		filter *dbtypes.MevBlockFilter
		match  func(b *dbtypes.MevBlock) bool
	}{
		{"all", &dbtypes.MevBlockFilter{}, func(b *dbtypes.MevBlock) bool { return true }},
		{"slot range", &dbtypes.MevBlockFilter{MinSlot: 305, MaxSlot: 320}, func(b *dbtypes.MevBlock) bool {
			return b.SlotNumber >= 305 && b.SlotNumber <= 320
		}},
		{"proposer range", &dbtypes.MevBlockFilter{MinIndex: 2, MaxIndex: 2}, func(b *dbtypes.MevBlock) bool { return b.ProposerIndex == 2 }},
		{"proposed", &dbtypes.MevBlockFilter{Proposed: []uint8{1, 2}}, func(b *dbtypes.MevBlock) bool { return b.Proposed == 1 || b.Proposed == 2 }},
		{"builder", &dbtypes.MevBlockFilter{BuilderPubkey: testPubkey(10)}, func(b *dbtypes.MevBlock) bool { return b.SlotNumber%2 == 0 }},
		{"relays", &dbtypes.MevBlockFilter{MevRelay: []uint8{0, 2}}, func(b *dbtypes.MevBlock) bool { return b.SeenbyRelays&0b101 != 0 }},
		{"proposer name", &dbtypes.MevBlockFilter{ProposerName: "beta"}, func(b *dbtypes.MevBlock) bool { return b.ProposerIndex == 1 }},
		{"combined", &dbtypes.MevBlockFilter{MinSlot: 303, Proposed: []uint8{0}, MevRelay: []uint8{1}}, func(b *dbtypes.MevBlock) bool {
			return b.SlotNumber >= 303 && b.Proposed == 0 && b.SeenbyRelays&0b10 != 0
		}},
		{"no match", &dbtypes.MevBlockFilter{MinSlot: 900}, func(b *dbtypes.MevBlock) bool { return false }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkPagedQuery(t, func(offset uint64, limit uint32) ([]uint64, uint64, error) {
				rows, count, err := GetMevBlocksFiltered(context.Background(), offset, limit, c.filter)
				ranks := make([]uint64, len(rows))
				for i, row := range rows {
					ranks[i] = row.SlotNumber
				}
				return ranks, count, err
			}, expect(c.match))
		})
	}
}
