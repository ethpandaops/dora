package models

import (
	"time"
)

// ExitsPageData is a struct to hold info for the exits overview page
type ExitsPageData struct {
	TotalVoluntaryExitCount uint64    `json:"total_voluntary_exit_count"`
	TotalRequestedExitCount uint64    `json:"total_requested_exit_count"`
	ExitingValidatorCount   uint64    `json:"exiting_validator_count"`
	ExitingAmount           uint64    `json:"exiting_amount"`
	QueueDurationEstimate   time.Time `json:"queue_duration_estimate"`
	HasQueueDuration        bool      `json:"has_queue_duration"`

	TabView string `json:"tab_view"`

	RecentExits     []*ExitsPageDataRecentExit `json:"recent_exits"`
	RecentExitCount uint64                     `json:"recent_exit_count"`

	ExitingValidators        []*ExitsPageDataExitingValidator `json:"exiting_validators"`
	ExitingValidatorTabCount uint64                           `json:"exiting_validator_tab_count"`

	RecentExitRequests     []*ExitsPageDataRecentExitRequest `json:"recent_exit_requests"`
	RecentExitRequestCount uint64                            `json:"recent_exit_request_count"`
}

type ExitsPageDataRecentExit struct {
	SlotNumber      uint64    `json:"slot"`
	SlotRoot        []byte    `json:"slot_root" ssz-size:"32"`
	Time            time.Time `json:"time"`
	Orphaned        bool      `json:"orphaned"`
	ValidatorIndex  uint64    `json:"vindex"`
	ValidatorName   string    `json:"vname"`
	IsBuilder       bool      `json:"is_builder"`
	PublicKey       []byte    `json:"pubkey" ssz-size:"48"`
	WithdrawalCreds []byte    `json:"wdcreds" ssz-size:"32"`
	ValidatorStatus string    `json:"vstatus"`
	ShowUpcheck     bool      `json:"show_upcheck"`
	UpcheckActivity uint8     `json:"upcheck_act"`
	UpcheckMaximum  uint8     `json:"upcheck_max"`
}

type ExitsPageDataExitingValidator struct {
	ValidatorIndex   uint64    `json:"validator_index"`
	ValidatorName    string    `json:"validator_name"`
	ValidatorStatus  string    `json:"validator_status"`
	PublicKey        []byte    `json:"pubkey" ssz-size:"48"`
	WithdrawalCreds  []byte    `json:"wdcreds" ssz-size:"32"`
	EffectiveBalance uint64    `json:"effective_balance"`
	ExitEpoch        uint64    `json:"exit_epoch"`
	EstimatedTime    time.Time `json:"estimated_time"`
	ShowUpcheck      bool      `json:"show_upcheck"`
	UpcheckActivity  uint8     `json:"upcheck_act"`
	UpcheckMaximum   uint8     `json:"upcheck_max"`
}

// ExitsPageDataRecentExitRequest represents an EL-triggered exit request.
type ExitsPageDataRecentExitRequest struct {
	IsIncluded        bool      `json:"is_included"`
	SlotNumber        uint64    `json:"slot"`
	SlotRoot          []byte    `json:"slot_root" ssz-size:"32"`
	Time              time.Time `json:"time"`
	Status            uint64    `json:"status"`
	Result            uint8     `json:"result"`
	ResultMessage     string    `json:"result_message"`
	TxStatus          uint64    `json:"tx_status"`
	SourceAddr        []byte    `json:"source_addr" ssz-size:"20"`
	ValidatorValid    bool      `json:"vvalid"`
	ValidatorIndex    uint64    `json:"vindex"`
	ValidatorName     string    `json:"vname"`
	PublicKey         []byte    `json:"pubkey" ssz-size:"48"`
	LinkedTransaction bool      `json:"linked_tx"`
	TransactionHash   []byte    `json:"tx_hash" ssz-size:"32"`
}
