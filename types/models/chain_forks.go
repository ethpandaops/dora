package models

// ChainForksPageData is a struct to hold info for the chain forks visualization page
type ChainForksPageData struct {
	Forks         []*ChainFork     `json:"forks"`
	StartSlot     uint64           `json:"start_slot"`
	EndSlot       uint64           `json:"end_slot"`
	StartEpoch    uint64           `json:"start_epoch"`
	EndEpoch      uint64           `json:"end_epoch"`
	PageSize      uint64           `json:"page_size"`
	FinalitySlot  uint64           `json:"finality_slot"`
	PrevPageSlot  *uint64          `json:"prev_page_slot"`
	NextPageSlot  *uint64          `json:"next_page_slot"`
	ChainDiagram  *ChainDiagram    `json:"chain_diagram"`
	ChainSpecs    *ChainSpecs      `json:"chain_specs"`
}

// ChainSpecs contains chain specification values needed for visualization
type ChainSpecs struct {
	SlotsPerEpoch uint64 `json:"slots_per_epoch"`
	SecondsPerSlot uint64 `json:"seconds_per_slot"`
}

type ChainFork struct {
	ForkId        uint64                    `json:"fork_id"`
	BaseSlot      uint64                    `json:"base_slot"`
	BaseRoot      []byte                    `json:"base_root"`
	LeafSlot      uint64                    `json:"leaf_slot"`  // First block of this fork (where it diverged)
	LeafRoot      []byte                    `json:"leaf_root"`
	HeadSlot      uint64                    `json:"head_slot"`  // Current head of this fork
	HeadRoot      []byte                    `json:"head_root"`
	ParentFork    uint64                    `json:"parent_fork"`
	Participation float64                   `json:"participation"`        // Overall average participation
	ParticipationByEpoch []*EpochParticipation `json:"participation_by_epoch"` // Participation per epoch
	IsCanonical   bool                      `json:"is_canonical"`
	Length        uint64                    `json:"length"`
}

type EpochParticipation struct {
	Epoch         uint64  `json:"epoch"`
	Participation float64 `json:"participation"`
	SlotCount     uint64  `json:"slot_count"`
}

type ChainDiagram struct {
	Epochs        []uint64               `json:"epochs"`        // Changed from slots to epochs
	Forks         []*DiagramFork         `json:"forks"`
	CanonicalLine *DiagramCanonicalLine  `json:"canonical_line"`
}

type DiagramFork struct {
	ForkId        uint64                    `json:"fork_id"`
	BaseSlot      uint64                    `json:"base_slot"`
	BaseRoot      []byte                    `json:"base_root"`
	LeafSlot      uint64                    `json:"leaf_slot"`  // First block of fork (where it diverged)
	LeafRoot      []byte                    `json:"leaf_root"`
	HeadSlot      uint64                    `json:"head_slot"`  // Current head of this fork
	HeadRoot      []byte                    `json:"head_root"`
	Length        uint64                    `json:"length"`
	Participation float64                   `json:"participation"`          // Overall average
	ParticipationByEpoch []*EpochParticipation `json:"participation_by_epoch"` // Per epoch data
	Position      int                       `json:"position"` // Horizontal position from canonical line
	ParentFork    uint64                    `json:"parent_fork"`
}

type DiagramCanonicalLine struct {
	StartSlot uint64 `json:"start_slot"`
	EndSlot   uint64 `json:"end_slot"`
}