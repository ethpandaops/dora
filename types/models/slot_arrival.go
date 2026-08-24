package models

// SlotArrivalResponse is the JSON payload returned by the lazy block
// propagation tab on the slot page, built from Xatu observations. Nodes merge
// two series: beacon API block events and libp2p gossipsub block messages.
type SlotArrivalResponse struct {
	Slot uint64 `json:"slot"`
	// Settled is false while the Xatu ingest pipeline may still be receiving
	// events for this slot; such responses are never cached.
	Settled          bool                    `json:"settled"`
	Observations     uint32                  `json:"observations"`
	P2PObservations  uint32                  `json:"p2p_observations"`
	HeadObservations uint32                  `json:"head_observations"`
	NPObservations   uint32                  `json:"np_observations"`
	Stats            *SlotArrivalStats       `json:"stats,omitempty"`
	Nodes            []*SlotArrivalNode      `json:"nodes,omitempty"`
	Continents       []*SlotArrivalContinent `json:"continents,omitempty"`
	Groups           []*SlotArrivalGroup     `json:"groups,omitempty"`
}

// SlotArrivalStats summarizes block propagation across observing nodes, using
// each node's earliest observation from either series.
type SlotArrivalStats struct {
	UniqueNodes uint32 `json:"unique_nodes"`
	MinMs       uint32 `json:"min_ms"`
	P50Ms       uint32 `json:"p50_ms"`
	P90Ms       uint32 `json:"p90_ms"`
	MaxMs       uint32 `json:"max_ms"`
	// LateNodes counts nodes whose earliest observation came more than a full
	// slot after slot start (syncing or stalled nodes); they are excluded
	// from the timing stats.
	LateNodes uint32 `json:"late_nodes"`
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
	Observations   uint32  `json:"observations"`
}

// SlotArrivalContinent aggregates earliest observations per continent.
type SlotArrivalContinent struct {
	Code  string `json:"code"`
	Nodes uint32 `json:"nodes"`
	MinMs uint32 `json:"min_ms"`
	P50Ms uint32 `json:"p50_ms"`
	P90Ms uint32 `json:"p90_ms"`
	MaxMs uint32 `json:"max_ms"`
}

// SlotArrivalGroup aggregates earliest observations per node group.
type SlotArrivalGroup struct {
	Name  string `json:"name"`
	Nodes uint32 `json:"nodes"`
	P50Ms uint32 `json:"p50_ms"`
}
