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
}

type ExitsPageDataRecentExit struct {
	SlotNumber      uint64    `json:"slot"`
	SlotRoot        []byte    `json:"slot_root"`
	Time            time.Time `json:"time"`
	Orphaned        bool      `json:"orphaned"`
	ValidatorIndex  uint64    `json:"vindex"`
	ValidatorName   string    `json:"vname"`
	PublicKey       []byte    `json:"pubkey"`
	WithdrawalCreds []byte    `json:"wdcreds"`
	ValidatorStatus string    `json:"vstatus"`
	ShowUpcheck     bool      `json:"show_upcheck"`
	UpcheckActivity uint8     `json:"upcheck_act"`
	UpcheckMaximum  uint8     `json:"upcheck_max"`
}

type ExitsPageDataExitingValidator struct {
	ValidatorIndex   uint64    `json:"validator_index"`
	ValidatorName    string    `json:"validator_name"`
	ValidatorStatus  string    `json:"validator_status"`
	PublicKey        []byte    `json:"pubkey"`
	WithdrawalCreds  []byte    `json:"wdcreds"`
	EffectiveBalance uint64    `json:"effective_balance"`
	ExitEpoch        uint64    `json:"exit_epoch"`
	EstimatedTime    time.Time `json:"estimated_time"`
	ShowUpcheck      bool      `json:"show_upcheck"`
	UpcheckActivity  uint8     `json:"upcheck_act"`
	UpcheckMaximum   uint8     `json:"upcheck_max"`
}
