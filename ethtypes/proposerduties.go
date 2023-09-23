package ethtypes

import (
	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type ProposerDuties struct {
	DependentRoot phase0.Root        `json:"dependent_root"`
	Data          []*v1.ProposerDuty `json:"data"`
}
