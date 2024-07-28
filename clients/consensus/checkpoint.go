package consensus

import "github.com/attestantio/go-eth2-client/spec/phase0"

type FinalizedCheckpoint struct {
	Epoch phase0.Epoch
	Root  phase0.Root
}
