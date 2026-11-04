package handlers

import (
	"testing"

	"github.com/ethpandaops/dora/clients/consensus"
)

func TestBlobStorageCalculatorRequiresPeerDASSpec(t *testing.T) {
	if got := blobStorageCalculator(nil); got == nil || got.TotalColumns != 0 {
		t.Fatalf("nil chain specs: got calculator %+v", got)
	}

	columns, custody, validatorCustody := uint64(128), uint64(4), uint64(8)
	complete := consensus.ChainSpec{}
	complete.NumberOfColumns = &columns
	complete.CustodyRequirement = &custody
	complete.ValidatorCustodyRequirement = &validatorCustody
	complete.MaxEffectiveBalance = 32e9
	complete.FieldElementsPerCell = 64
	complete.SlotsPerEpoch = 32
	complete.MinEpochsForBlobSidecarsRequests = 4096

	for _, tc := range []struct {
		name string
		omit func(*consensus.ChainSpec)
	}{
		{"number of columns", func(spec *consensus.ChainSpec) { spec.NumberOfColumns = nil }},
		{"custody requirement", func(spec *consensus.ChainSpec) { spec.CustodyRequirement = nil }},
		{"validator custody requirement", func(spec *consensus.ChainSpec) { spec.ValidatorCustodyRequirement = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := complete
			tc.omit(&spec)
			if got := blobStorageCalculator(&spec); got == nil || got.TotalColumns != 0 {
				t.Fatalf("missing %s: got calculator %+v", tc.name, got)
			}
		})
	}

	zeroColumns := complete
	zero := uint64(0)
	zeroColumns.NumberOfColumns = &zero
	if got := blobStorageCalculator(&zeroColumns); got == nil || got.TotalColumns != 0 {
		t.Fatalf("zero columns: got calculator %+v", got)
	}
	zeroBalance := complete
	zeroBalance.MaxEffectiveBalance = 0
	if got := blobStorageCalculator(&zeroBalance); got == nil || got.TotalColumns != 0 {
		t.Fatalf("zero effective balance: got calculator %+v", got)
	}

	got := blobStorageCalculator(&complete)
	if got == nil {
		t.Fatal("complete PeerDAS specs: calculator missing")
	}
	if got.MaxEth != 4096 || got.DefaultEth != 32 || got.TotalColumns != 128 || got.CustodyRequirement != 4 || got.ValidatorCustodyRequirement != 8 {
		t.Errorf("complete PeerDAS specs: unexpected calculator %+v", got)
	}
}
