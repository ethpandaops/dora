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
	Aggregates       []*SlotAggregate       `json:"aggregates,omitempty"`
	AggregateMembers uint64                 `json:"aggregate_members,omitempty"`
	AggregateCovered uint64                 `json:"aggregate_covered,omitempty"`
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
	Name                string                   `json:"name"`
	Continent           string                   `json:"continent,omitempty"`
	Country             string                   `json:"country,omitempty"`
	CountryCode         string                   `json:"country_code,omitempty"`
	Expected            uint64                   `json:"expected"`
	Seen                uint64                   `json:"seen"`
	AfterAttestationDue uint64                   `json:"after_attestation_due"`
	AfterDue            uint64                   `json:"after_due"`
	Late                uint64                   `json:"late"`
	P50Ms               uint32                   `json:"p50_ms"`
	P90Ms               uint32                   `json:"p90_ms"`
	Buckets             []*SlotAttestationBucket `json:"buckets,omitempty"`
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
	Aggregates       *EpochAggregates       `json:"aggregates,omitempty" ssz-type:"optional"`
}

type AttestersNodeResponse struct {
	Node             string                    `json:"node"`
	AttestationDueMs uint32                    `json:"attestation_due_ms"`
	AggregateDueMs   uint32                    `json:"aggregate_due_ms"`
	Total            uint64                    `json:"total"`
	Validators       []*AttestersNodeValidator `json:"validators,omitempty"`
}

type AttestersNodeValidator struct {
	Index  uint64 `json:"index"`
	Slot   uint64 `json:"slot"`
	Seen   bool   `json:"seen"`
	SeenMs uint32 `json:"seen_ms"`
}

type SlotAggregate struct {
	CommitteeIndex  uint64 `json:"committee_index"`
	AggregatorIndex uint64 `json:"aggregator_index"`
	AggregatorName  string `json:"aggregator_name,omitempty"`
	Continent       string `json:"continent,omitempty"`
	Country         string `json:"country,omitempty"`
	CountryCode     string `json:"country_code,omitempty"`
	Client          string `json:"client"`
	Seen            bool   `json:"seen"`
	Redundant       bool   `json:"redundant,omitempty"`
	SeenMs          uint32 `json:"seen_ms"`
	SizeBytes       uint32 `json:"size_bytes"`
	Observers       uint64 `json:"observers"`
	ObserversP50Ms  uint32 `json:"observers_p50_ms"`
	ObserversShared bool   `json:"observers_shared,omitempty"`
	Attesters       uint64 `json:"attesters"`
	CommitteeSize   uint64 `json:"committee_size"`
	LastIncludedMs  uint32 `json:"last_included_ms"`
	IncludedUnseen  uint64 `json:"included_unseen"`
	LeftOut         uint64 `json:"left_out"`
}

type EpochAggregates struct {
	Aggregates uint64                   `json:"aggregates"`
	Missing    uint64                   `json:"missing"`
	Redundant  uint64                   `json:"redundant"`
	SizeBytes  uint64                   `json:"size_bytes"`
	Members    uint64                   `json:"members"`
	Covered    uint64                   `json:"covered"`
	Slots      []*EpochAggregateSlot    `json:"slots"`
	Clients    []*EpochAggregatorClient `json:"clients"`
	Points     []*EpochAggregatePoint   `json:"points"`
}

type EpochAggregatePoint struct {
	Slot          uint64 `json:"slot"`
	Committee     uint64 `json:"committee"`
	Aggregator    uint64 `json:"aggregator"`
	Node          string `json:"node,omitempty"`
	Client        string `json:"client"`
	Seen          bool   `json:"seen"`
	Redundant     bool   `json:"redundant,omitempty"`
	Ms            uint32 `json:"ms"`
	Attesters     uint64 `json:"attesters"`
	CommitteeSize uint64 `json:"committee_size"`
}

type EpochAggregateSlot struct {
	Slot       uint64  `json:"slot"`
	Committees uint64  `json:"committees"`
	Aggregates uint64  `json:"aggregates"`
	Missing    uint64  `json:"missing"`
	Redundant  uint64  `json:"redundant"`
	Members    uint64  `json:"members"`
	Covered    uint64  `json:"covered"`
	FirstMs    *uint32 `json:"first_ms,omitempty" ssz-type:"optional"`
}

type EpochAggregatorClient struct {
	Name             string                 `json:"name"`
	Aggregates       uint64                 `json:"aggregates"`
	Missing          uint64                 `json:"missing"`
	Redundant        uint64                 `json:"redundant"`
	Late             uint64                 `json:"late"`
	Attesters        uint64                 `json:"attesters"`
	CommitteeMembers uint64                 `json:"committee_members"`
	P50Ms            uint32                 `json:"p50_ms"`
	P90Ms            uint32                 `json:"p90_ms"`
	Nodes            []*EpochAggregatorNode `json:"nodes,omitempty"`
}

type EpochAggregatorNode struct {
	Name             string `json:"name"`
	Continent        string `json:"continent,omitempty"`
	Country          string `json:"country,omitempty"`
	CountryCode      string `json:"country_code,omitempty"`
	Aggregates       uint64 `json:"aggregates"`
	Missing          uint64 `json:"missing"`
	Redundant        uint64 `json:"redundant"`
	Late             uint64 `json:"late"`
	Attesters        uint64 `json:"attesters"`
	CommitteeMembers uint64 `json:"committee_members"`
	P50Ms            uint32 `json:"p50_ms"`
	P90Ms            uint32 `json:"p90_ms"`
}
