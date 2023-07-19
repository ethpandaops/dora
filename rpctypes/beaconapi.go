package rpctypes

type StandardV1BeaconHeaderResponse struct {
	Finalized bool `json:"finalized"`
	Data      struct {
		Root      string                  `json:"root"`
		Canonical bool                    `json:"canonical"`
		Header    SignedBeaconBlockHeader `json:"header"`
	} `json:"data"`
}

type StandardV1BeaconHeadersResponse struct {
	Finalized bool `json:"finalized"`
	Data      []struct {
		Root      string                  `json:"root"`
		Canonical bool                    `json:"canonical"`
		Header    SignedBeaconBlockHeader `json:"header"`
	} `json:"data"`
}

type StandardV2BeaconBlockResponse struct {
	Finalized bool              `json:"finalized"`
	Data      SignedBeaconBlock `json:"data"`
}

type CombinedBlockResponse struct {
	Header *StandardV1BeaconHeaderResponse
	Block  *StandardV2BeaconBlockResponse
}

type StandardV1ProposerDutiesResponse struct {
	DependentRoot string `json:"dependent_root"`
	Data          []struct {
		Pubkey         string    `json:"pubkey"`
		ValidatorIndex Uint64Str `json:"validator_index"`
		Slot           Uint64Str `json:"slot"`
	} `json:"data"`
}

type StandardV1CommitteesResponse struct {
	Data []struct {
		Index      Uint64Str `json:"index"`
		Slot       Uint64Str `json:"slot"`
		Validators []string  `json:"validators"`
	} `json:"data"`
}

type StandardV1SyncCommitteesResponse struct {
	Data struct {
		Validators          []string   `json:"validators"`
		ValidatorAggregates [][]string `json:"validator_aggregates"`
	} `json:"data"`
}

type EpochAssignments struct {
	ProposerAssignments map[uint64]uint64
	AttestorAssignments map[string][]uint64
	SyncAssignments     []uint64
}
