package models

// TracoorBeaconBlock represents a beacon block trace from Tracoor
type TracoorBeaconBlock struct {
	ID                   string `json:"id"`
	Node                 string `json:"node"`
	FetchedAt            string `json:"fetched_at"`
	Slot                 string `json:"slot"`
	Epoch                string `json:"epoch"`
	BlockRoot            string `json:"block_root"`
	NodeVersion          string `json:"node_version"`
	Network              string `json:"network"`
	BeaconImplementation string `json:"beacon_implementation"`
}

// TracoorBeaconBlockResponse is the response from Tracoor list-beacon-block API
type TracoorBeaconBlockResponse struct {
	BeaconBlocks []TracoorBeaconBlock `json:"beacon_blocks"`
}

// TracoorBeaconState represents a beacon state trace from Tracoor
type TracoorBeaconState struct {
	ID                   string `json:"id"`
	Node                 string `json:"node"`
	FetchedAt            string `json:"fetched_at"`
	Slot                 string `json:"slot"`
	Epoch                string `json:"epoch"`
	StateRoot            string `json:"state_root"`
	NodeVersion          string `json:"node_version"`
	Network              string `json:"network"`
	BeaconImplementation string `json:"beacon_implementation"`
}

// TracoorBeaconStateResponse is the response from Tracoor list-beacon-state API
type TracoorBeaconStateResponse struct {
	BeaconStates []TracoorBeaconState `json:"beacon_states"`
}

// TracoorExecutionBlockTrace represents an execution block trace from Tracoor
type TracoorExecutionBlockTrace struct {
	ID                      string `json:"id"`
	Node                    string `json:"node"`
	FetchedAt               string `json:"fetched_at"`
	BlockHash               string `json:"block_hash"`
	BlockNumber             string `json:"block_number"`
	Network                 string `json:"network"`
	ExecutionImplementation string `json:"execution_implementation"`
	NodeVersion             string `json:"node_version"`
}

// TracoorExecutionBlockTraceResponse is the response from Tracoor list-execution-block-trace API
type TracoorExecutionBlockTraceResponse struct {
	ExecutionBlockTraces []TracoorExecutionBlockTrace `json:"execution_block_traces"`
}

// SlotTracoorData represents the combined Tracoor data for a slot
type SlotTracoorData struct {
	TracoorUrl             string                       `json:"tracoor_url"`
	BeaconBlocks           []TracoorBeaconBlock         `json:"beacon_blocks"`
	BeaconStates           []TracoorBeaconState         `json:"beacon_states"`
	ExecutionBlockTraces   []TracoorExecutionBlockTrace `json:"execution_block_traces"`
	BeaconBlocksError      string                       `json:"beacon_blocks_error,omitempty"`
	BeaconStatesError      string                       `json:"beacon_states_error,omitempty"`
	ExecutionBlockTraceErr string                       `json:"execution_block_trace_error,omitempty"`
}
