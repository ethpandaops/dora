package models

// EpochArrivalResponse is the JSON payload for the epoch page's per-slot
// block arrival summary, built from Xatu observations.
type EpochArrivalResponse struct {
	Epoch uint64 `json:"epoch"`
	// Settled is false while the Xatu ingest pipeline may still be receiving
	// events for slots in this epoch; such responses are never cached.
	Settled bool                         `json:"settled"`
	Slots   map[uint64]*EpochArrivalSlot `json:"slots"`
}

// EpochArrivalSlot summarizes one slot's block arrival across observing
// nodes, using each node's earliest observation.
type EpochArrivalSlot struct {
	Nodes uint32 `json:"nodes"`
	MinMs uint32 `json:"min_ms"`
	P90Ms uint32 `json:"p90_ms"`
}
