package models

import (
	"time"
)

// WithdrawalsPageData is a struct to hold info for the withdrawals overview page
type WithdrawalsPageData struct {
	WithdrawnAmount24h    float64   `json:"withdrawn_amount_24h"`
	TotalWithdrawalCount  uint64    `json:"total_withdrawal_count"`
	QueuedWithdrawalCount uint64    `json:"queued_withdrawal_count"`
	WithdrawingAmount     uint64    `json:"withdrawing_amount"`
	QueueDurationEstimate time.Time `json:"queue_duration_estimate"`
	HasQueueDuration      bool      `json:"has_queue_duration"`

	TabView string `json:"tab_view"`

	RecentWithdrawals     []*WithdrawalsPageDataRecentWithdrawal `json:"recent_withdrawals"`
	RecentWithdrawalCount uint64                                 `json:"recent_withdrawal_count"`

	QueuedWithdrawals []*WithdrawalsPageDataQueuedWithdrawal `json:"queued_withdrawals"`
	QueuedTabCount    uint64                                 `json:"queued_tab_count"`

	BeaconWithdrawals     []*WithdrawalsPageDataBeaconWithdrawal `json:"beacon_withdrawals"`
	BeaconWithdrawalCount uint64                                 `json:"beacon_withdrawal_count"`
}

type WithdrawalsPageDataRecentWithdrawal struct {
	IsIncluded        bool      `json:"is_included"`
	SlotNumber        uint64    `json:"slot"`
	SlotRoot          []byte    `json:"slot_root" ssz-size:"32"`
	Time              time.Time `json:"time"`
	Status            uint64    `json:"status"`
	Result            uint8     `json:"result"`
	ResultMessage     string    `json:"result_message"`
	TxStatus          uint64    `json:"tx_status"`
	SourceAddr        []byte    `json:"source_addr" ssz-size:"20"`
	Amount            uint64    `json:"amount"`
	ValidatorValid    bool      `json:"vvalid"`
	ValidatorIndex    uint64    `json:"vindex"`
	ValidatorName     string    `json:"vname"`
	PublicKey         []byte    `json:"pubkey" ssz-size:"48"`
	LinkedTransaction bool      `json:"linked_tx"`
	TransactionHash   []byte    `json:"tx_hash" ssz-size:"32"`
}

type WithdrawalsPageDataQueuedWithdrawal struct {
	ValidatorIndex    uint64    `json:"validator_index"`
	ValidatorName     string    `json:"validator_name"`
	ValidatorStatus   string    `json:"validator_status"`
	PublicKey         []byte    `json:"pubkey" ssz-size:"48"`
	Amount            uint64    `json:"amount"`
	WithdrawableEpoch uint64    `json:"withdrawable_epoch"`
	EstimatedTime     time.Time `json:"estimated_time"`
	ShowUpcheck       bool      `json:"show_upcheck"`
	UpcheckActivity   uint8     `json:"upcheck_act"`
	UpcheckMaximum    uint8     `json:"upcheck_max"`
}

// WithdrawalsPageDataBeaconWithdrawal represents a beacon chain withdrawal on the overview page.
type WithdrawalsPageDataBeaconWithdrawal struct {
	SlotNumber     uint64    `json:"slot"`
	BlockRoot      []byte    `json:"block_root" ssz-size:"32"`
	BlockNumber    uint64    `json:"block_number"`
	Time           time.Time `json:"time"`
	Orphaned       bool      `json:"orphaned"`
	Type           uint8     `json:"type"`
	HasValidator   bool      `json:"has_validator"`
	ValidatorIndex uint64    `json:"vindex"`
	ValidatorName  string    `json:"vname"`
	Address        []byte    `json:"address" ssz-size:"20"`
	Amount         float64   `json:"amount"`
	AmountRaw      []byte    `json:"amount_raw" ssz-size:"32"`
}
