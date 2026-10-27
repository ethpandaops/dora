package services

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/executionproof"
	"github.com/ethpandaops/dora/executionproof/verifiers"
)

// GetBlockExecutionProofs returns the EIP-8025 execution proofs collected for a block,
// in proof type order: the proofs the indexer still holds in memory for a recent block,
// otherwise the ones stored in the slot's blockdb meta object (the unfinalized_metadata
// row where no blockdb takes the objects over). The proof bytes are only loaded if
// withData is set. Returns nil if no proofs are known for the block.
func (bs *ChainService) GetBlockExecutionProofs(ctx context.Context, slot phase0.Slot, blockRoot phase0.Root, withData bool) []*btypes.SlotExecutionProof {
	if cached := bs.beaconIndexer.GetBlockExecutionProofs(blockRoot); len(cached) > 0 {
		return cached
	}

	flags := btypes.SlotMetaFlagExecutionProofs
	if withData {
		flags |= btypes.SlotMetaFlagExecutionProofData
	}

	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsSlotMeta() {
		return bs.beaconIndexer.GetUnfinalizedExecutionProofs(ctx, slot, blockRoot, withData)
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

// ExecutionProofAnalysis is what the explorer can tell about one execution proof
// without a zkVM verifier: its registered type, the values it commits to, and the
// checks of those values and its prover signature against the block.
type ExecutionProofAnalysis struct {
	Proof *btypes.SlotExecutionProof
	// Type is the registered proof type, nil for an unassigned one.
	Type *executionproof.Type
	// Committed is what the proof bytes reveal, nil if they could not be read
	// (DecodeError says why).
	Committed   *executionproof.Committed
	DecodeError string
	// PublicInputMatch tells whether the committed public input is the one the
	// block expects; nil if either side is unknown.
	PublicInputMatch *bool
	// ProgramVKMatch tells whether the program key the proof carries is the
	// registered one; nil if the proof carries none.
	ProgramVKMatch *bool
	// SignatureValid tells whether the prover signature verified when the proof
	// was collected, ProverActive whether the prover was an active validator at
	// the block's epoch.
	SignatureValid bool
	ProverActive   bool
	// Verification is the outcome of verifying the proof with the embedded
	// zkVM verifiers when it was collected, Verifier the verifier release.
	Verification verifiers.Status
	Verifier     string
}

// BlockExecutionProofsAnalysis is the analysis of all execution proofs of a block.
type BlockExecutionProofsAnalysis struct {
	// Expected is the public input a valid proof of the block commits to, nil
	// if it cannot be derived (ExpectedError says why).
	Expected      *executionproof.PublicInput
	ExpectedError string
	Proofs        []*ExecutionProofAnalysis
}

// AnalyzeBlockExecutionProofs interprets the execution proofs of a block: it
// derives the public input the block's payload expects and reads what each proof
// commits to. The prover signature and zkVM verification outcomes come with the
// proof (both done when it was collected).
func (bs *ChainService) AnalyzeBlockExecutionProofs(ctx context.Context, slot phase0.Slot, blockRoot phase0.Root) *BlockExecutionProofsAnalysis {
	analysis := &BlockExecutionProofsAnalysis{}

	proofs := bs.GetBlockExecutionProofs(ctx, slot, blockRoot, true)
	if len(proofs) == 0 {
		return analysis
	}

	specs := bs.consensusPool.GetChainState().GetSpecs()
	analysis.Expected, analysis.ExpectedError = bs.expectedExecutionProofPublicInput(ctx, blockRoot, specs.DepositChainId)

	analysis.Proofs = make([]*ExecutionProofAnalysis, 0, len(proofs))
	for _, proof := range proofs {
		entry := &ExecutionProofAnalysis{Proof: proof, Type: executionproof.Lookup(proof.ProofType)}

		if entry.Type == nil {
			entry.DecodeError = "unassigned proof type"
		} else if proof.ProofData == nil {
			entry.DecodeError = "proof bytes not available"
		} else if committed, err := decodeCommitted(entry.Type.ZkVM, proof); err != nil {
			entry.DecodeError = err.Error()
		} else {
			entry.Committed = committed
			if committed.PublicInput != nil && analysis.Expected != nil {
				match := *committed.PublicInput == *analysis.Expected
				entry.PublicInputMatch = &match
			}
			if committed.ProgramVK != nil {
				match := bytes.Equal(committed.ProgramVK, entry.Type.ProgramVK)
				entry.ProgramVKMatch = &match
			}
		}

		// the prover signature was checked against the block's validator set when
		// the proof was collected; the outcome travels with the proof
		entry.SignatureValid = proof.SignatureValid
		entry.ProverActive = proof.ProverActive
		entry.Verification = verifiers.Status(proof.Verification)
		entry.Verifier = proof.Verifier

		analysis.Proofs = append(analysis.Proofs, entry)
	}

	return analysis
}

// expectedExecutionProofPublicInput derives the public input a valid execution
// proof of the block commits to from the block's payload envelope.
func (bs *ChainService) expectedExecutionProofPublicInput(ctx context.Context, blockRoot phase0.Root, chainID uint64) (*executionproof.PublicInput, string) {
	block, err := bs.GetSlotDetailsByBlockroot(ctx, blockRoot)
	if err != nil {
		return nil, fmt.Sprintf("block not available: %v", err)
	}
	if block == nil {
		return nil, "block not available"
	}
	if block.Block == nil {
		return nil, "block body not available"
	}
	if block.Payload == nil {
		return nil, "execution payload envelope not available"
	}
	schemaID, ok := executionproof.SchemaIDForFork(block.Block.Version)
	if !ok {
		return nil, fmt.Sprintf("no stateless input schema assigned to the %s fork", block.Block.Version)
	}

	request, err := executionproof.BlockNewPayloadRequest(block.Block, block.Payload)
	if err != nil {
		return nil, err.Error()
	}
	root, err := request.Root(bs.beaconIndexer.GetDynSSZ())
	if err != nil {
		return nil, fmt.Sprintf("new payload request root: %v", err)
	}

	return &executionproof.PublicInput{
		NewPayloadRequestRoot: root,
		SuccessfulValidation:  true,
		ChainID:               chainID,
		SchemaID:              schemaID,
	}, ""
}

// decodeCommitted reads the committed values of a proof: as zkboost mock proof
// for a proof accepted as one, else with the decoder of its zkVM.
func decodeCommitted(zkvm executionproof.ZkVM, proof *btypes.SlotExecutionProof) (*executionproof.Committed, error) {
	if proof.Verifier == executionproof.MockVerifier {
		return executionproof.DecodeMock(proof.ProofData)
	}

	return executionproof.DecodeCommitted(zkvm, proof.ProofData)
}
