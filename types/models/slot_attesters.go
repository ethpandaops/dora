package models

type SlotAttestersResponse struct {
	Slot             uint64                 `json:"slot"`
	Settled          bool                   `json:"settled"`
	SlotMs           uint32                 `json:"slot_ms"`
	BucketMs         uint32                 `json:"bucket_ms"`
	AttestationDueMs uint32                 `json:"attestation_due_ms"`
	AggregateDueMs   uint32                 `json:"aggregate_due_ms"`
	Expected         uint64                 `json:"expected"`
	Seen             uint64                 `json:"seen"`
	OtherRoots       uint64                 `json:"other_roots"`
	Clients          []*SlotAttestersClient `json:"clients,omitempty"`
}

type SlotAttestersClient struct {
	Name                string                   `json:"name"`
	Expected            uint64                   `json:"expected"`
	Seen                uint64                   `json:"seen"`
	AfterAttestationDue uint64                   `json:"after_attestation_due"`
	AfterDue            uint64                   `json:"after_due"`
	Late                uint64                   `json:"late"`
	P50Ms               uint32                   `json:"p50_ms"`
	P90Ms               uint32                   `json:"p90_ms"`
	Buckets             []*SlotAttestationBucket `json:"buckets,omitempty"`
	Nodes               []*SlotAttestersNode     `json:"nodes,omitempty"`
}

type SlotAttestersNode struct {
	Name                string `json:"name"`
	Expected            uint64 `json:"expected"`
	Seen                uint64 `json:"seen"`
	AfterAttestationDue uint64 `json:"after_attestation_due"`
	AfterDue            uint64 `json:"after_due"`
	Late                uint64 `json:"late"`
	P50Ms               uint32 `json:"p50_ms"`
	P90Ms               uint32 `json:"p90_ms"`
}

type EpochAttestersResponse struct {
	Epoch            uint64                 `json:"epoch"`
	Settled          bool                   `json:"settled"`
	SlotMs           uint32                 `json:"slot_ms"`
	BucketMs         uint32                 `json:"bucket_ms"`
	AttestationDueMs uint32                 `json:"attestation_due_ms"`
	AggregateDueMs   uint32                 `json:"aggregate_due_ms"`
	Expected         uint64                 `json:"expected"`
	Seen             uint64                 `json:"seen"`
	Clients          []*SlotAttestersClient `json:"clients,omitempty"`
}
