package models

// SlotArrivalResponse is the JSON payload returned by the lazy block arrival
// callout on the slot page, built from Xatu block event observations.
type SlotArrivalResponse struct {
	Slot uint64 `json:"slot"`
	// Settled is false while the Xatu ingest pipeline may still be receiving
	// events for this slot; such responses are never cached.
	Settled      bool                    `json:"settled"`
	Observations int                     `json:"observations"`
	Stats        *SlotArrivalStats       `json:"stats,omitempty"`
	Clients      []*SlotArrivalClient    `json:"clients,omitempty"`
	Continents   []*SlotArrivalContinent `json:"continents,omitempty"`
}

// SlotArrivalStats summarizes block propagation across all observing clients,
// using each client's first (deduplicated) observation.
type SlotArrivalStats struct {
	UniqueClients int    `json:"unique_clients"`
	MinMs         uint32 `json:"min_ms"`
	P50Ms         uint32 `json:"p50_ms"`
	P90Ms         uint32 `json:"p90_ms"`
	MaxMs         uint32 `json:"max_ms"`
}

// SlotArrivalClient is one observing client's first block observation.
type SlotArrivalClient struct {
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	Continent      string `json:"continent"`
	MinMs          uint32 `json:"min_ms"`
	Observations   int    `json:"observations"`
}

// SlotArrivalContinent aggregates first observations per continent.
type SlotArrivalContinent struct {
	Code    string `json:"code"`
	Clients int    `json:"clients"`
	MinMs   uint32 `json:"min_ms"`
}
