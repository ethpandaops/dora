package indexer

import "github.com/attestantio/go-eth2-client/spec"

func getDepositIndexFromState(state *spec.VersionedBeaconState) uint64 {
	switch state.Version {
	case spec.DataVersionPhase0:
		return state.Phase0.ETH1DepositIndex
	case spec.DataVersionAltair:
		return state.Altair.ETH1DepositIndex
	case spec.DataVersionBellatrix:
		return state.Bellatrix.ETH1DepositIndex
	case spec.DataVersionCapella:
		return state.Capella.ETH1DepositIndex
	case spec.DataVersionDeneb:
		return state.Deneb.ETH1DepositIndex
	}
	return 0
}
