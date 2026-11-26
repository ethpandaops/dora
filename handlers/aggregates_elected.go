package handlers

import (
	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
)

func electedAggregators(slot phase0.Slot) [][]phase0.ValidatorIndex {
	return services.GlobalBeaconService.GetBeaconIndexer().GetAggregationCommittees(slot)
}
