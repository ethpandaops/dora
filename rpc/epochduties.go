package rpc

import (
	"fmt"
	"math"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/pk910/dora-the-explorer/utils"
)

type EpochAssignments struct {
	DependendRoot       phase0.Root         `json:"dep_root"`
	DependendStateRef   string              `json:"dep_state"`
	ProposerAssignments map[uint64]uint64   `json:"prop"`
	AttestorAssignments map[string][]uint64 `json:"att"`
	SyncAssignments     []uint64            `json:"sync"`
}

// GetEpochAssignments will get the epoch assignments from Lighthouse RPC api
func (bc *BeaconClient) GetEpochAssignments(epoch uint64, dependendRoot []byte) (*EpochAssignments, error) {
	parsedProposerResponse, err := bc.GetProposerDuties(epoch)
	if err != nil {
		return nil, err
	}

	if parsedProposerResponse != nil {
		dependendRoot = parsedProposerResponse.DependentRoot[:]
	}
	if dependendRoot == nil {
		return nil, fmt.Errorf("couldn't find dependent root for epoch %v", epoch)
	}

	var depStateRoot string
	// fetch the block root that the proposer data is dependent on
	parsedHeader, err := bc.GetBlockHeaderByBlockroot(dependendRoot)
	if err != nil {
		return nil, err
	}
	depStateRoot = parsedHeader.Header.Message.StateRoot.String()
	if epoch == 0 {
		depStateRoot = "genesis"
	}

	assignments := &EpochAssignments{
		DependendRoot:       phase0.Root(dependendRoot),
		DependendStateRef:   depStateRoot,
		ProposerAssignments: make(map[uint64]uint64),
		AttestorAssignments: make(map[string][]uint64),
	}

	// proposer duties
	if utils.Config.Chain.WhiskForkEpoch != nil && epoch >= *utils.Config.Chain.WhiskForkEpoch {
		firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
		lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
		for slot := firstSlot; slot <= lastSlot; slot++ {
			assignments.ProposerAssignments[slot] = math.MaxInt64
		}
	} else if parsedProposerResponse != nil {
		for _, duty := range parsedProposerResponse.Data {
			assignments.ProposerAssignments[uint64(duty.Slot)] = uint64(duty.ValidatorIndex)
		}
	}

	// Now use the state root to make a consistent committee query
	parsedCommittees, err := bc.GetCommitteeDuties(depStateRoot, epoch)
	if err != nil {
		logger.Errorf("error retrieving committees data: %v", err)
	} else {
		// attester duties
		for _, committee := range parsedCommittees {
			for i, valIndex := range committee.Validators {
				valIndexU64 := uint64(valIndex)
				if err != nil {
					return nil, fmt.Errorf("epoch %d committee %d index %d has bad validator index %q", epoch, committee.Index, i, valIndex)
				}
				k := fmt.Sprintf("%v-%v", uint64(committee.Slot), uint64(committee.Index))
				if assignments.AttestorAssignments[k] == nil {
					assignments.AttestorAssignments[k] = make([]uint64, 0)
				}
				assignments.AttestorAssignments[k] = append(assignments.AttestorAssignments[k], valIndexU64)
			}
		}
	}

	if epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncCommitteeState := depStateRoot
		if epoch > 0 && epoch == utils.Config.Chain.Config.AltairForkEpoch {
			syncCommitteeState = fmt.Sprintf("%d", utils.Config.Chain.Config.AltairForkEpoch*utils.Config.Chain.Config.SlotsPerEpoch)
		}
		parsedSyncCommittees, err := bc.GetSyncCommitteeDuties(syncCommitteeState, epoch)
		if err != nil {
			logger.Errorf("error retrieving sync_committees for epoch %v (state: %v): %v", epoch, syncCommitteeState, err)
		} else {
			assignments.SyncAssignments = make([]uint64, len(parsedSyncCommittees.Validators))

			// sync committee duties
			for i, valIndexStr := range parsedSyncCommittees.Validators {
				valIndexU64 := uint64(valIndexStr)
				if err != nil {
					return nil, fmt.Errorf("in sync_committee for epoch %d validator %d has bad validator index: %q", epoch, i, valIndexStr)
				}
				assignments.SyncAssignments[i] = valIndexU64
			}
		}
	}

	return assignments, nil
}
