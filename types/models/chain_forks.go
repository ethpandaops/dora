package models

// ChainForksPageData is a struct to hold minimal info for the chain forks visualization page template
type ChainForksPageData struct {
	ChainSpecs *ChainSpecs `json:"chain_specs"`
}

// ChainSpecs contains chain specification values needed for visualization
type ChainSpecs struct {
	SlotsPerEpoch  uint64 `json:"slots_per_epoch"`
	SecondsPerSlot uint64 `json:"seconds_per_slot"`
	GenesisTime    uint64 `json:"genesis_time"`   // Genesis time in unix timestamp
	CurrentSlot    uint64 `json:"current_slot"`   // Current head slot
	EpochsFor12h   uint64 `json:"epochs_for_12h"` // Pre-calculated epoch counts for time selectors
	EpochsFor1d    uint64 `json:"epochs_for_1d"`
	EpochsFor7d    uint64 `json:"epochs_for_7d"`
	EpochsFor14d   uint64 `json:"epochs_for_14d"`
}

// ChainForksDiagramData contains all the data needed for AJAX diagram requests
type ChainForksDiagramData struct {
	Diagram             *ChainDiagram `json:"diagram"`
	StartSlot           uint64        `json:"start_slot"`
	EndSlot             uint64        `json:"end_slot"`
	StartEpoch          uint64        `json:"start_epoch"`
	EndEpoch            uint64        `json:"end_epoch"`
	FinalitySlot        uint64        `json:"finality_slot"`
	RequestedStartSlot  uint64        `json:"requested_start_slot"`  // Original requested start
	RequestedSizeEpochs uint64        `json:"requested_size_epochs"` // Original requested size
	PrevPageSlot        *uint64       `json:"prev_page_slot"`
	NextPageSlot        *uint64       `json:"next_page_slot"`
	Error               string        `json:"error,omitempty"` // Error message if something went wrong
}

type ChainFork struct {
	ForkId               uint64                `json:"fork_id"`
	BaseSlot             uint64                `json:"base_slot"`
	BaseRoot             []byte                `json:"base_root"`
	LeafSlot             uint64                `json:"leaf_slot"` // First block of this fork (where it diverged)
	LeafRoot             []byte                `json:"leaf_root"`
	HeadSlot             uint64                `json:"head_slot"` // Current head of this fork
	HeadRoot             []byte                `json:"head_root"`
	ParentFork           uint64                `json:"parent_fork"`
	Participation        float64               `json:"participation"`          // Overall average participation
	ParticipationByEpoch []*EpochParticipation `json:"participation_by_epoch"` // Participation per epoch
	IsCanonical          bool                  `json:"is_canonical"`
	Length               uint64                `json:"length"`      // Number of slots in the fork
	BlockCount           uint64                `json:"block_count"` // Actual number of blocks in the fork
}

type EpochParticipation struct {
	Epoch         uint64  `json:"epoch"`
	Participation float64 `json:"participation"`
	SlotCount     uint64  `json:"slot_count"`
}

type ChainDiagram struct {
	Epochs        []uint64              `json:"epochs"` // Changed from slots to epochs
	Forks         []*DiagramFork        `json:"forks"`
	CanonicalLine *DiagramCanonicalLine `json:"canonical_line"`
}

type DiagramFork struct {
	ForkId               uint64                `json:"fork_id"`
	BaseSlot             uint64                `json:"base_slot"`
	BaseRoot             []byte                `json:"base_root"`
	LeafSlot             uint64                `json:"leaf_slot"` // First block of fork (where it diverged)
	LeafRoot             []byte                `json:"leaf_root"`
	HeadSlot             uint64                `json:"head_slot"` // Current head of this fork
	HeadRoot             []byte                `json:"head_root"`
	Length               uint64                `json:"length"`
	BlockCount           uint64                `json:"block_count"`            // Actual number of blocks
	Participation        float64               `json:"participation"`          // Overall average
	ParticipationByEpoch []*EpochParticipation `json:"participation_by_epoch"` // Per epoch data
	ParentFork           uint64                `json:"parent_fork"`
	IsCanonical          bool                  `json:"is_canonical"` // Whether this is part of canonical chain
}

type DiagramCanonicalLine struct {
	StartSlot uint64 `json:"start_slot"`
	EndSlot   uint64 `json:"end_slot"`
}
