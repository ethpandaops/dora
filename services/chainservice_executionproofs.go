package services

import (
	"context"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
)

// GetBlockExecutionProofs returns the EIP-8025 execution proofs collected for a block,
// in proof type order: the proofs the indexer still holds in memory for a recent block,
// otherwise the ones stored in the slot's blockdb meta object. The proof bytes are
// only loaded if withData is set. Returns nil if no proofs are known for the block.
func (bs *ChainService) GetBlockExecutionProofs(ctx context.Context, slot phase0.Slot, blockRoot phase0.Root, withData bool) []*btypes.SlotExecutionProof {
	if cached := bs.beaconIndexer.GetBlockExecutionProofs(blockRoot); len(cached) > 0 {
		return cached
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsSlotMeta() {
		return nil
	}

	flags := btypes.SlotMetaFlagExecutionProofs
	if withData {
		flags |= btypes.SlotMetaFlagExecutionProofData
	}

	stored, err := blockdb.GlobalBlockDb.GetSlotMeta(ctx, uint64(slot), flags)
	if err != nil {
		bs.logger.Warnf("error loading meta object for slot %d: %v", slot, err)
		return nil
	}
	if stored == nil {
		return nil
	}

	proofs := make([]*btypes.SlotExecutionProof, 0, len(stored.ExecutionProofs))
	for _, proof := range stored.ExecutionProofs {
		if proof.BlockRoot == blockRoot {
			proofs = append(proofs, proof)
		}
	}

	return proofs
}
