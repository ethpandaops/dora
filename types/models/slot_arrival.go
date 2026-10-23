package models

// The per-series arrival fields are optional: a node that reported gossip but
// no block event must render as an em dash, not as 0ms. The page cache stores
// this model as SSZ, so every such pointer carries ssz-type:"optional" to keep
// nil distinct from zero. cache.TestNilPointerFidelityThroughCache guards this.
//
// SlotArrivalResponse is the JSON payload returned by the lazy block
// propagation tab on the slot page, built from Xatu observations. Nodes merge
// two series: beacon API block events and libp2p gossipsub block messages.
type SlotArrivalResponse struct {
	Slot uint64 `json:"slot"`
	// Settled is false while the Xatu ingest pipeline may still be receiving
	// events for this slot; such responses are cached for one slot only.
	Settled          bool                    `json:"settled"`
	Observations     uint64                  `json:"observations"`
	P2PObservations  uint64                  `json:"p2p_observations"`
	HeadObservations uint64                  `json:"head_observations"`
	NPObservations   uint64                  `json:"np_observations"`
	PLObservations   uint64                  `json:"pl_observations"`
	Stats            *SlotArrivalStats       `json:"stats,omitempty" ssz-type:"optional"`
	Nodes            []*SlotArrivalNode      `json:"nodes,omitempty"`
	Continents       []*SlotArrivalContinent `json:"continents,omitempty"`
	Groups           []*SlotArrivalGroup     `json:"groups,omitempty"`
}

// SlotArrivalStats summarizes block propagation across observing nodes, using
// each node's earliest observation from either series.
type SlotArrivalStats struct {
	UniqueNodes uint64 `json:"unique_nodes"`
	MinMs       uint32 `json:"min_ms"`
	P50Ms       uint32 `json:"p50_ms"`
	P90Ms       uint32 `json:"p90_ms"`
	MaxMs       uint32 `json:"max_ms"`
	// LateNodes counts nodes whose earliest observation came more than a full
	// slot after slot start (syncing or stalled nodes); they are excluded
	// from the timing stats.
	LateNodes uint64 `json:"late_nodes"`
}

// SlotArrivalNode is one observing node with its earliest observation per
// series. Group is one of "ethpandaops", "community", "corp" or "other".
type SlotArrivalNode struct {
	Name           string  `json:"name"`
	FullName       string  `json:"full_name"`
	Group          string  `json:"group"`
	Operator       string  `json:"operator,omitempty"`
	Implementation string  `json:"implementation,omitempty"`
	Continent      string  `json:"continent,omitempty"`
	Country        string  `json:"country,omitempty"`
	CountryCode    string  `json:"country_code,omitempty"`
	MinMs          uint32  `json:"min_ms"`
	APIMs          *uint32 `json:"api_ms,omitempty" ssz-type:"optional"`
	P2PMs          *uint32 `json:"p2p_ms,omitempty" ssz-type:"optional"`
	HeadMs         *uint32 `json:"head_ms,omitempty" ssz-type:"optional"`
	NPMs           *uint32 `json:"np_ms,omitempty" ssz-type:"optional"`
	PLMs           *uint32 `json:"pl_ms,omitempty" ssz-type:"optional"`
	NPDurMs        *uint32 `json:"np_dur_ms,omitempty" ssz-type:"optional"`
	NPStatus       string  `json:"np_status,omitempty"`
	Late           bool    `json:"late,omitempty"`
	Observations   uint64  `json:"observations"`
}

// SlotArrivalContinent aggregates per-continent arrival stats for the two
// observable halves of the slot: when nodes adopted the block as head and
// when they first saw the execution payload. A series is nil when the
// continent has no observations of it (payload stats only exist on networks
// with the gloas payload gossip schema).
type SlotArrivalContinent struct {
	Code      string  `json:"code"`
	Nodes     uint64  `json:"nodes"`
	HeadMinMs *uint32 `json:"head_min_ms,omitempty" ssz-type:"optional"`
	HeadP50Ms *uint32 `json:"head_p50_ms,omitempty" ssz-type:"optional"`
	HeadP90Ms *uint32 `json:"head_p90_ms,omitempty" ssz-type:"optional"`
	PlMinMs   *uint32 `json:"pl_min_ms,omitempty" ssz-type:"optional"`
	PlP50Ms   *uint32 `json:"pl_p50_ms,omitempty" ssz-type:"optional"`
	PlP90Ms   *uint32 `json:"pl_p90_ms,omitempty" ssz-type:"optional"`
}

// SlotArrivalGroup aggregates earliest observations per node group.
type SlotArrivalGroup struct {
	Name  string `json:"name"`
	Nodes uint64 `json:"nodes"`
	P50Ms uint32 `json:"p50_ms"`
}
