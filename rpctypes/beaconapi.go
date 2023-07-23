package rpctypes

type StandardV1StreamedBlockEvent struct {
	Slot                Uint64Str   `json:"slot"`
	Block               BytesHexStr `json:"block"`
	ExecutionOptimistic bool        `json:"execution_optimistic"`
}

type StandardV1StreamedHeadEvent struct {
	Slot                      Uint64Str   `json:"slot"`
	Block                     BytesHexStr `json:"block"`
	State                     BytesHexStr `json:"state"`
	EpochTransition           bool        `json:"epoch_transition"`
	PreviousDutyDependentRoot BytesHexStr `json:"previous_duty_dependent_root"`
	CurrentDutyDependentRoot  BytesHexStr `json:"current_duty_dependent_root"`
	ExecutionOptimistic       bool        `json:"execution_optimistic"`
}

type StandardV1BeaconHeaderResponse struct {
	Finalized bool `json:"finalized"`
	Data      struct {
		Root      BytesHexStr             `json:"root"`
		Canonical bool                    `json:"canonical"`
		Header    SignedBeaconBlockHeader `json:"header"`
	} `json:"data"`
}

type StandardV1BeaconHeadersResponse struct {
	Finalized bool `json:"finalized"`
	Data      []struct {
		Root      BytesHexStr             `json:"root"`
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
	DependentRoot BytesHexStr `json:"dependent_root"`
	Data          []struct {
		Pubkey         BytesHexStr `json:"pubkey"`
		ValidatorIndex Uint64Str   `json:"validator_index"`
		Slot           Uint64Str   `json:"slot"`
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
	DependendRoot       BytesHexStr         `json:"dep_root"`
	DependendState      BytesHexStr         `json:"dep_state"`
	ProposerAssignments map[uint64]uint64   `json:"prop"`
	AttestorAssignments map[string][]uint64 `json:"att"`
	SyncAssignments     []uint64            `json:"sync"`
}

type StandardV1StateValidatorsResponse struct {
	Data []struct {
		Index     Uint64Str `json:"index"`
		Balance   Uint64Str `json:"balance"`
		Status    string    `json:"status"`
		Validator Validator `json:"validator"`
	} `json:"data"`
}
