package db

import (
	"context"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// TestGetDepositsFilteredByValidatorName checks the validator name filter, which
// resolves names through the validators table (deposits are keyed by pubkey).
func TestGetDepositsFilteredByValidatorName(t *testing.T) {
	newTestDB(t)

	pubkey1 := append([]byte{0x01}, make([]byte, 47)...)
	pubkey2 := append([]byte{0x02}, make([]byte, 47)...)
	pubkeyUnknown := append([]byte{0x03}, make([]byte, 47)...)
	creds := make([]byte, 32)

	err := RunDBTransaction(func(tx *sqlx.Tx) error {
		for i, pubkey := range [][]byte{pubkey1, pubkey2} {
			if err := InsertValidator(context.Background(), tx, &dbtypes.Validator{
				ValidatorIndex: uint64(i), Pubkey: pubkey, WithdrawalCredentials: creds,
			}); err != nil {
				return err
			}
		}
		if err := InsertValidatorNames(context.Background(), tx, []*dbtypes.ValidatorName{
			{Index: 0, Name: "lighthouse-geth-1"},
			{Index: 1, Name: "prysm-besu-2"},
		}); err != nil {
			return err
		}

		for i, pubkey := range [][]byte{pubkey1, pubkey2, pubkeyUnknown} {
			depositIndex := uint64(i)
			if err := InsertDeposits(context.Background(), tx, []*dbtypes.Deposit{{
				Index: &depositIndex, SlotNumber: uint64(100 + i), SlotIndex: 0,
				SlotRoot: []byte{byte(i)}, PublicKey: pubkey, WithdrawalCredentials: creds,
				Amount: 32000000000,
			}}); err != nil {
				return err
			}
			if err := InsertDepositTxs(context.Background(), tx, []*dbtypes.DepositTx{{
				Index: depositIndex, BlockNumber: uint64(10 + i), BlockRoot: []byte{byte(i)},
				PublicKey: pubkey, WithdrawalCredentials: creds, Amount: 32000000000,
				Signature: []byte{}, TxHash: []byte{byte(i)}, TxSender: []byte{}, TxTarget: []byte{},
				ValidSignature: 1,
			}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name      string
		vname     string
		wantCount int
	}{
		{"matches single validator", "lighthouse-geth-1", 1},
		{"substring match", "prysm", 1},
		{"no match", "unknown-node", 0},
		{"no filter returns all", "", 3},
	}

	for _, c := range cases {
		t.Run("deposits: "+c.name, func(t *testing.T) {
			deposits, _, err := GetDepositsFiltered(context.Background(), 0, 100, []uint64{0},
				&dbtypes.DepositFilter{ValidatorName: c.vname, WithOrphaned: 1}, nil)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(deposits) != c.wantCount {
				t.Errorf("got %v deposits, want %v", len(deposits), c.wantCount)
			}
		})
		t.Run("deposit_txs: "+c.name, func(t *testing.T) {
			txs, _, err := GetDepositTxsFiltered(context.Background(), 0, 100, []uint64{0},
				&dbtypes.DepositTxFilter{ValidatorName: c.vname, WithOrphaned: 1, WithValid: 1})
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(txs) != c.wantCount {
				t.Errorf("got %v deposit txs, want %v", len(txs), c.wantCount)
			}
		})
	}
}
