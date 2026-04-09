package statetransition

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/pk910/dynamic-ssz/sszutils"
)

// ApplyBlock applies a beacon block to the state in-place (process_block).
// The state must be at the block's slot (call PrepareEpochPreState or ProcessSlots first).
// After this call, the state matches the block's post-state (pre-payload for Gloas).
//
// If parentStateRoot is non-zero, it is used as the hint for the first
// process_slot's state-root caching, skipping the expensive HTR computation.
// This is safe when the caller already knows the HTR of the current state
// (e.g., from the parent block's state_root field). Subsequent process_slot
// calls (when there are skipped slots) still compute HTR normally.
//
// Modified in Gloas: https://github.com/ethereum/consensus-specs/blob/master/specs/gloas/beacon-chain.md#modified-block-processing
func applyBlockInternal(state *spec.VersionedBeaconState, block *spec.VersionedSignedBeaconBlock, specs *consensus.ChainSpec, ds *dynssz.DynSsz, caches *stateTransitionCaches, parentStateRoot phase0.Root) error {
	if state.Version < spec.DataVersionFulu {
		return nil
	}

	s, err := newStateAccessorWithCaches(state, specs, caches)
	if err != nil {
		return fmt.Errorf("failed to create state accessor: %w", err)
	}

	blockSlot, err := block.Slot()
	if err != nil {
		return fmt.Errorf("failed to get block slot: %w", err)
	}

	// Advance state to the block's slot via process_slots.
	// This implements the spec's process_slots exactly:
	//   while state.slot < slot:
	//       process_slot(state)
	//       if (state.slot + 1) % SLOTS_PER_EPOCH == 0:
	//           process_epoch(state)
	//       state.slot += 1
	// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#process_slots
	slotsPerEpoch := specs.SlotsPerEpoch
	stateRootHint := parentStateRoot
	for s.Slot < blockSlot {
		if err := processSlotRootCaching(s, stateRootHint); err != nil {
			return fmt.Errorf("process_slot at slot %d: %w", s.Slot, err)
		}
		// Hint only applies to the first iteration; subsequent slots have
		// mutated state and need fresh HTR.
		stateRootHint = phase0.Root{}
		if (uint64(s.Slot)+1)%slotsPerEpoch == 0 {
			if err := processEpochInternal(s, nil); err != nil {
				return fmt.Errorf("process_epoch at slot %d: %w", s.Slot, err)
			}
		}
		s.Slot++
		s.writeBack()
	}

	proposerIndex, err := block.ProposerIndex()
	if err != nil {
		return fmt.Errorf("failed to get proposer index: %w", err)
	}

	parentRoot, err := block.ParentRoot()
	if err != nil {
		return fmt.Errorf("failed to get parent root: %w", err)
	}

	bodyRoot, err := getBlockBodyRoot(block)
	if err != nil {
		return fmt.Errorf("failed to get body root: %w", err)
	}

	// process_block_header
	processBlockHeader(s, blockSlot, proposerIndex, parentRoot, bodyRoot)

	// process_withdrawals
	processWithdrawals(s)

	// process_execution_payload (Fulu) — caches the execution payload header
	if state.Version == spec.DataVersionFulu {
		processFuluExecutionPayload(s, block, ds)
	}

	// process_execution_payload_bid (Gloas) — records the builder's bid
	if state.Version >= spec.DataVersionGloas {
		processExecutionPayloadBid(s, block)
	}

	// process_randao
	processRandao(s, block)

	// process_eth1_data
	processEth1Data(s, block)

	// process_operations
	processOperations(s, block)

	// process_sync_aggregate
	processSyncAggregate(s, block)

	s.writeBack()

	return nil
}

// processSlotRootCaching implements process_slot: caches state root and block root.
// If stateRootHint is non-zero it is used directly, skipping the expensive HTR
// computation. The caller is responsible for ensuring the hint matches the
// current state's HTR (e.g., by sourcing it from the parent block's state_root).
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#process_slot
func processSlotRootCaching(s *stateAccessor, stateRootHint phase0.Root) error {
	// Cache state root — use hint if provided, otherwise compute HTR.
	var stateRoot phase0.Root
	if stateRootHint != (phase0.Root{}) {
		stateRoot = stateRootHint
	} else {
		var err error
		stateRoot, err = s.computeStateHTR()
		if err != nil {
			return fmt.Errorf("failed to compute state root: %w", err)
		}
	}

	idx := uint64(s.Slot) % s.specs.SlotsPerHistoricalRoot
	s.StateRoots[idx] = stateRoot

	// Fill in latest block header's state root if it's the default zero value
	if s.LatestBlockHeader != nil && s.LatestBlockHeader.StateRoot == (phase0.Root{}) {
		s.LatestBlockHeader.StateRoot = stateRoot
	}

	// Cache block root
	blockRoot, err := s.computeLatestBlockHeaderHTR()
	if err != nil {
		return fmt.Errorf("failed to compute block root: %w", err)
	}

	s.BlockRoots[idx] = blockRoot

	// Gloas: clear the next slot's execution payload availability bit.
	s.clearNextSlotAvailabilityBit()

	return nil
}

// processBlockHeader implements process_block_header.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#block-header
func processBlockHeader(s *stateAccessor, slot phase0.Slot, proposerIndex phase0.ValidatorIndex, parentRoot phase0.Root, bodyRoot phase0.Root) {
	header := &phase0.BeaconBlockHeader{
		Slot:          slot,
		ProposerIndex: proposerIndex,
		ParentRoot:    parentRoot,
		StateRoot:     phase0.Root{}, // filled in by next process_slot
		BodyRoot:      bodyRoot,
	}

	s.LatestBlockHeader = header
}

// processRandao mixes in the block's RANDAO reveal.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#randao
func processRandao(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	randaoReveal, err := block.RandaoReveal()
	if err != nil {
		return
	}

	epoch := s.currentEpoch()
	idx := uint64(epoch) % s.specs.EpochsPerHistoricalVector

	// Mix in: XOR current mix with hash of the reveal
	revealHash := sha256.Sum256(randaoReveal[:])
	var mixed phase0.Root
	for i := 0; i < 32; i++ {
		mixed[i] = s.RANDAOMixes[idx][i] ^ revealHash[i]
	}
	s.RANDAOMixes[idx] = mixed
}

// processEth1Data adds the block's ETH1 vote.
// https://github.com/ethereum/consensus-specs/blob/master/specs/phase0/beacon-chain.md#eth1-data
func processEth1Data(s *stateAccessor, block *spec.VersionedSignedBeaconBlock) {
	eth1Data, err := block.ETH1Data()
	if err != nil || eth1Data == nil {
		return
	}

	s.ETH1DataVotes = append(s.ETH1DataVotes, eth1Data)

	// Check if majority vote
	voteCount := 0
	for _, vote := range s.ETH1DataVotes {
		if bytes.Equal(vote.BlockHash, eth1Data.BlockHash) &&
			vote.DepositRoot == eth1Data.DepositRoot &&
			vote.DepositCount == eth1Data.DepositCount {
			voteCount++
		}
	}

	threshold := s.specs.EpochsPerEth1VotingPeriod * s.specs.SlotsPerEpoch / 2
	if uint64(voteCount) > threshold {
		s.ETH1Data = eth1Data
	}
}

// Transaction/Withdrawal list types for SSZ hash_tree_root computation via dynamic-ssz.
type transactionList []bellatrix.Transaction

var _ = sszutils.Annotate[transactionList](`ssz-max:"1048576,1073741824" ssz-size:"?,?"`)

type withdrawalList []*capella.Withdrawal

var _ = sszutils.Annotate[withdrawalList](`ssz-max:"16"`)

// processFuluExecutionPayload caches the execution payload header for Fulu blocks.
// https://github.com/ethereum/consensus-specs/blob/master/specs/fulu/beacon-chain.md#modified-process_execution_payload
func processFuluExecutionPayload(s *stateAccessor, block *spec.VersionedSignedBeaconBlock, ds *dynssz.DynSsz) {
	if block.Fulu == nil || block.Fulu.Message == nil || block.Fulu.Message.Body == nil {
		return
	}

	payload := block.Fulu.Message.Body.ExecutionPayload
	if payload == nil {
		return
	}

	if ds == nil {
		return
	}

	txs := make(transactionList, len(payload.Transactions))
	copy(txs, payload.Transactions)
	txRoot, err := ds.HashTreeRoot(txs)
	if err != nil {
		return
	}

	wds := make(withdrawalList, len(payload.Withdrawals))
	copy(wds, payload.Withdrawals)
	wdRoot, err := ds.HashTreeRoot(wds)
	if err != nil {
		return
	}

	s.LatestExecutionPayloadHeader = &deneb.ExecutionPayloadHeader{
		ParentHash:       payload.ParentHash,
		FeeRecipient:     payload.FeeRecipient,
		StateRoot:        payload.StateRoot,
		ReceiptsRoot:     payload.ReceiptsRoot,
		LogsBloom:        payload.LogsBloom,
		PrevRandao:       payload.PrevRandao,
		BlockNumber:      payload.BlockNumber,
		GasLimit:         payload.GasLimit,
		GasUsed:          payload.GasUsed,
		Timestamp:        payload.Timestamp,
		ExtraData:        payload.ExtraData,
		BaseFeePerGas:    payload.BaseFeePerGas,
		BlockHash:        payload.BlockHash,
		TransactionsRoot: phase0.Root(txRoot),
		WithdrawalsRoot:  phase0.Root(wdRoot),
		BlobGasUsed:      payload.BlobGasUsed,
		ExcessBlobGas:    payload.ExcessBlobGas,
	}
}

// getBlockBodyRoot computes the body root of a signed beacon block.
func getBlockBodyRoot(block *spec.VersionedSignedBeaconBlock) (phase0.Root, error) {
	switch block.Version {
	case spec.DataVersionFulu:
		if block.Fulu != nil && block.Fulu.Message != nil && block.Fulu.Message.Body != nil {
			return block.Fulu.Message.Body.HashTreeRoot()
		}
	case spec.DataVersionGloas:
		if block.Gloas != nil && block.Gloas.Message != nil && block.Gloas.Message.Body != nil {
			return block.Gloas.Message.Body.HashTreeRoot()
		}
	}
	return phase0.Root{}, fmt.Errorf("unsupported block version: %v", block.Version)
}
