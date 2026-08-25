package models

// SlotWavesResponse is the JSON payload for the xatu-cbt backed panels on the
// slot page's propagation tab: the attestation first-seen wave and the data
// column propagation strip. Either section is nil when its table has no rows
// for the slot. The count fields are deliberately plain int, which keeps the
// whole model on the page cache's JSON path, where nil sections survive a
// cache round-trip; see the note on SlotArrivalResponse.
type SlotWavesResponse struct {
	Slot uint64 `json:"slot"`
	// Settled is false while the cbt transformations may still be rewriting
	// this slot's rows; such responses are cached for one slot only.
	Settled bool `json:"settled"`
	// SlotMs is the slot duration, so the charts can span the whole slot
	// instead of stopping wherever the data does.
	SlotMs       uint32               `json:"slot_ms"`
	Attestations *SlotAttestationWave `json:"attestations,omitempty"`
	Columns      *SlotColumnWave      `json:"columns,omitempty"`
}

// SlotAttestationWave is the network's attestation traffic for one slot,
// bucketed into 50ms chunks by when each attestation was first seen by any
// observing node, and split by the block root the attestations vote for.
type SlotAttestationWave struct {
	TotalCount int `json:"total_count"`
	// DeadlineMs is a third of the slot, the point where validators that have
	// not seen a block attest anyway, which the wave visibly spikes around.
	DeadlineMs uint32 `json:"deadline_ms"`
	// Roots is ordered by vote count, largest first. Low-volume roots beyond
	// the first few are merged into one entry with an empty Root.
	Roots []*SlotAttestationRoot `json:"roots,omitempty"`
}

// SlotAttestationRoot is one voted block root's share of the wave.
type SlotAttestationRoot struct {
	Root string `json:"root"`
	// Viewed marks the root of the block the page is showing, so the template
	// can separate votes for this block from wrong-head votes.
	Viewed  bool                     `json:"viewed,omitempty"`
	Count   int                      `json:"count"`
	Buckets []*SlotAttestationBucket `json:"buckets,omitempty"`
}

// SlotAttestationBucket is one 50ms chunk: attestations first seen between Ms
// and Ms+50 after slot start.
type SlotAttestationBucket struct {
	Ms    uint32 `json:"ms"`
	Count int    `json:"count"`
}

// SlotColumnWave describes how the slot's data column sidecars spread and how
// available they measured afterwards, one entry per column index. Timings are
// reduced from every observing node's sighting, so each column carries its
// own percentiles rather than one network-first value.
type SlotColumnWave struct {
	BlobCount   int `json:"blob_count"`
	SeenColumns int `json:"seen_columns"`
	// Observations counts every node-column sighting behind the percentiles.
	Observations int `json:"observations"`
	// MedianMs is the median of all sightings across all columns.
	MedianMs uint32 `json:"median_ms"`
	// FirstMs and LastMs span the per-column first-seen times.
	FirstMs uint32 `json:"first_ms"`
	LastMs  uint32 `json:"last_ms"`
	// Availability aggregates over the probed columns.
	ProbedColumns   int           `json:"probed_columns"`
	AvgAvailability float64       `json:"avg_availability"`
	MinAvailability float64       `json:"min_availability"`
	TotalProbes     int           `json:"total_probes"`
	WorstP50Ms      uint32        `json:"worst_p50_ms"`
	Columns         []*SlotColumn `json:"columns,omitempty"`
}

// SlotColumn is one data column's propagation and availability measurements.
// FirstSeenMs and AvailabilityPct are optional because the two series come
// from different tables: a column can be seen but never probed and the other
// way round.
type SlotColumn struct {
	Index       uint32  `json:"index"`
	FirstSeenMs *uint32 `json:"first_seen_ms,omitempty"`
	// P50Ms and P90Ms are percentiles of when each observing node saw this
	// column; FirstSeenMs is their minimum.
	P50Ms        uint32 `json:"p50_ms,omitempty"`
	P90Ms        uint32 `json:"p90_ms,omitempty"`
	Observations int    `json:"observations,omitempty"`
	// FirstSeenBy is the display name of the node that saw the column first.
	FirstSeenBy     string   `json:"first_seen_by,omitempty"`
	Implementation  string   `json:"implementation,omitempty"`
	CountryCode     string   `json:"country_code,omitempty"`
	AvailabilityPct *float64 `json:"availability_pct,omitempty"`
	Probes          int      `json:"probes,omitempty"`
	P50ResponseMs   uint32   `json:"p50_response_ms,omitempty"`
}
