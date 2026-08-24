package models

// The per-series arrival fields are optional: a node that reported gossip but
// no block event must render as an em dash, not as 0ms. SSZ has no optionals
// and round-trips a nil pointer back as a pointer to zero, so the count fields
// below are deliberately plain int, which keeps the whole model on the page
// cache's JSON path. cache.TestNilPointerFidelityThroughCache guards this.
//
// SlotArrivalResponse is the JSON payload returned by the lazy block
// propagation tab on the slot page, built from Xatu observations. Nodes merge
// two series: beacon API block events and libp2p gossipsub block messages.
type SlotArrivalResponse struct {
	Slot uint64 `json:"slot"`
	// Settled is false while the Xatu ingest pipeline may still be receiving
	// events for this slot; such responses are cached for one slot only.
	Settled          bool                    `json:"settled"`
	Observations     int                     `json:"observations"`
	P2PObservations  int                     `json:"p2p_observations"`
	HeadObservations int                     `json:"head_observations"`
	NPObservations   int                     `json:"np_observations"`
	Stats            *SlotArrivalStats       `json:"stats,omitempty"`
	Nodes            []*SlotArrivalNode      `json:"nodes,omitempty"`
	Continents       []*SlotArrivalContinent `json:"continents,omitempty"`
	Groups           []*SlotArrivalGroup     `json:"groups,omitempty"`
}

// SlotArrivalStats summarizes block propagation across observing nodes, using
// each node's earliest observation from either series.
type SlotArrivalStats struct {
	UniqueNodes int    `json:"unique_nodes"`
	MinMs       uint32 `json:"min_ms"`
	P50Ms       uint32 `json:"p50_ms"`
	P90Ms       uint32 `json:"p90_ms"`
	MaxMs       uint32 `json:"max_ms"`
	// LateNodes counts nodes whose earliest observation came more than a full
	// slot after slot start (syncing or stalled nodes); they are excluded
	// from the timing stats.
	LateNodes int `json:"late_nodes"`
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
	APIMs          *uint32 `json:"api_ms,omitempty"`
	P2PMs          *uint32 `json:"p2p_ms,omitempty"`
	HeadMs         *uint32 `json:"head_ms,omitempty"`
	NPMs           *uint32 `json:"np_ms,omitempty"`
	NPDurMs        *uint32 `json:"np_dur_ms,omitempty"`
	NPStatus       string  `json:"np_status,omitempty"`
	Late           bool    `json:"late,omitempty"`
	Observations   int     `json:"observations"`
}

// SlotArrivalContinent aggregates earliest observations per continent.
type SlotArrivalContinent struct {
	Code  string `json:"code"`
	Nodes int    `json:"nodes"`
	MinMs uint32 `json:"min_ms"`
	P50Ms uint32 `json:"p50_ms"`
	P90Ms uint32 `json:"p90_ms"`
	MaxMs uint32 `json:"max_ms"`
}

// SlotArrivalGroup aggregates earliest observations per node group.
type SlotArrivalGroup struct {
	Name  string `json:"name"`
	Nodes int    `json:"nodes"`
	P50Ms uint32 `json:"p50_ms"`
}
