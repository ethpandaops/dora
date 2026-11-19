package statetransition

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/go-eth2-client/spec/pq"
	dynssz "github.com/pk910/dynamic-ssz"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/utils"
)

func loadPQTestState(t *testing.T) (*consensus.ChainSpec, *dynssz.DynSsz, *all.BeaconState) {
	t.Helper()

	values := map[string]any{}
	for key, value := range specDomainTypes {
		values[key] = value
	}

	specData, err := os.ReadFile("testdata/pq_minimal_spec.yaml")
	if err != nil {
		t.Fatalf("failed to read spec: %v", err)
	}

	for key, value := range parseFlatYaml(string(specData)) {
		values[key] = value
	}

	parsed := utils.ParseSpecMap(values)
	specs := &consensus.ChainSpec{}

	if err := specs.ParseAdditive(parsed); err != nil {
		t.Fatalf("failed to parse spec: %v", err)
	}

	ds := dynssz.NewDynSsz(parsed)

	encoded, err := os.ReadFile("testdata/pq_genesis_minimal.ssz")
	if err != nil {
		t.Fatalf("failed to read state: %v", err)
	}

	state := &all.BeaconState{Version: spec.DataVersionPQ}
	if err := ds.UnmarshalSSZ(state, encoded); err != nil {
		t.Fatalf("failed to decode state: %v", err)
	}

	return specs, ds, state
}

func TestPQEpochTransition(t *testing.T) {
	specs, ds, state := loadPQTestState(t)

	if specs.MaxCommitteesPerSlotPq != 4 || !specs.IsPqActive(0) {
		t.Fatalf("pq fork not active in spec: %+v", specs.PqForkEpoch)
	}

	root, err := ds.HashTreeRoot(state)
	if err != nil {
		t.Fatalf("failed to hash state: %v", err)
	}

	if got := phase0.Root(root).String(); got != "0xcc0e5639191cf4d1b7d459b8e1d3a7d6aa86eaa37cc63b6f02a9ea6145c15736" {
		t.Fatalf("unexpected genesis state root %v", got)
	}

	if err := NewStateTransition(specs, ds).PrepareEpochPreState(state, 2, nil); err != nil {
		t.Fatalf("epoch transition failed: %v", err)
	}

	expectedData, err := os.ReadFile("testdata/pq_epoch2_prestate.json")
	if err != nil {
		t.Fatalf("failed to read expected state: %v", err)
	}

	var expected struct {
		Slot              uint64     `json:"slot"`
		ProposerLookahead []uint64   `json:"proposer_lookahead"`
		PTCWindow         [][]uint64 `json:"ptc_window"`
		EffectiveBalances []uint64   `json:"effective_balances"`
	}

	if err := json.Unmarshal(expectedData, &expected); err != nil {
		t.Fatalf("failed to parse expected state: %v", err)
	}

	if uint64(state.Slot) != expected.Slot {
		t.Fatalf("slot %v, expected %v", state.Slot, expected.Slot)
	}

	for i, proposer := range expected.ProposerLookahead {
		if uint64(state.ProposerLookahead[i]) != proposer {
			t.Fatalf("proposer lookahead %d: %v, expected %v", i, state.ProposerLookahead[i], proposer)
		}
	}

	for i, row := range expected.PTCWindow {
		for j, member := range row {
			if uint64(state.PTCWindow[i][j]) != member {
				t.Fatalf("ptc window %d/%d: %v, expected %v", i, j, state.PTCWindow[i][j], member)
			}
		}
	}

	for i, balance := range expected.EffectiveBalances {
		if uint64(state.Validators[i].EffectiveBalance) != balance || uint64(state.XMSSValidators[i].EffectiveBalance) != balance {
			t.Fatalf("effective balance %d: %v, expected %v", i, state.Validators[i].EffectiveBalance, balance)
		}
	}

	encoded, err := ds.MarshalSSZ(state)
	if err != nil {
		t.Fatalf("failed to encode state: %v", err)
	}

	reloaded := &all.BeaconState{Version: spec.DataVersionPQ}
	if err := ds.UnmarshalSSZ(reloaded, encoded); err != nil {
		t.Fatalf("failed to decode state: %v", err)
	}

	if len(reloaded.Validators) != len(state.Validators) {
		t.Fatalf("validator count changed after reload")
	}
}

func TestPQPendingDeposits(t *testing.T) {
	specs, ds, state := loadPQTestState(t)

	var xmssKey pq.XMSSPubKey

	xmssKey[0] = 0x42

	deposit := &electra.PendingDeposit{
		Pubkey:                xmssKey.DepositPubKey(),
		WithdrawalCredentials: append([]byte{0x01}, make([]byte, 31)...),
		Amount:                32_000_000_000,
	}
	deposit.Signature[0] = 0x99

	legacy := &electra.PendingDeposit{
		WithdrawalCredentials: append([]byte{0x01}, make([]byte, 31)...),
		Amount:                32_000_000_000,
	}
	legacy.Pubkey[0] = 0x8a

	topUp := &electra.PendingDeposit{
		Pubkey:                state.Validators[2].PublicKey,
		WithdrawalCredentials: state.Validators[2].WithdrawalCredentials,
		Amount:                1_000_000_000,
	}

	state.PendingDeposits = []*electra.PendingDeposit{deposit, legacy, topUp}
	balance := state.Balances[2]

	if err := NewStateTransition(specs, ds).PrepareEpochPreState(state, 1, nil); err != nil {
		t.Fatalf("epoch transition failed: %v", err)
	}

	if len(state.Validators) != 10 || len(state.XMSSValidators) != 10 || len(state.PendingDeposits) != 0 {
		t.Fatalf("unexpected registry: %d validators, %d xmss validators, %d pending deposits", len(state.Validators), len(state.XMSSValidators), len(state.PendingDeposits))
	}

	if state.XMSSValidators[8].PublicKey != xmssKey || state.XMSSValidators[8].VRFRoot[0] != 0x99 || state.XMSSValidators[8].EffectiveBalance != 32_000_000_000 {
		t.Fatalf("unexpected new validator: %+v", state.XMSSValidators[8])
	}

	if state.Validators[8].ExitEpoch != FarFutureEpoch {
		t.Fatalf("xmss deposit validator must not be exited")
	}

	if state.Validators[9].ExitEpoch != 0 || state.XMSSValidators[9].ExitEpoch != 0 || isLegacyPubkey(state.Validators[9].PublicKey) {
		t.Fatalf("legacy deposit validator must be exited at once under its derived key: %+v", state.Validators[9])
	}

	if state.Balances[2] <= balance {
		t.Fatalf("top-up was not applied")
	}

	if _, err := ds.HashTreeRoot(state); err != nil {
		t.Fatalf("failed to hash state: %v", err)
	}
}
