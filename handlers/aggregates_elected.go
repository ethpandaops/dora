package handlers

import (
	"context"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func electedAggregators(_ context.Context, _ phase0.Slot) [][]phase0.ValidatorIndex {
	return nil
}
