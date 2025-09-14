package models

import (
	"time"
)

// ConsolidationsPageData is a struct to hold info for the consolidations overview page
type ConsolidationsPageData struct {
	TotalConsolidationCount      uint64                                       `json:"total_consolidation_count"`
	ConsolidatingValidatorCount  uint64                                       `json:"consolidating_validator_count"`
	ConsolidatingAmount          uint64                                       `json:"consolidating_amount"`
	QueuedConsolidationCount     uint64                                       `json:"queued_consolidation_count"`
	QueueDurationEstimate        time.Time                                    `json:"queue_duration_estimate"`
	HasQueueDuration             bool                                         `json:"has_queue_duration"`
	RecentConsolidations         []*ConsolidationsPageDataRecentConsolidation `json:"recent_consolidations"`
	RecentConsolidationCount     uint64                                       `json:"recent_consolidation_count"`
	QueuedConsolidations     []*ConsolidationsPageDataQueuedConsolidation `json:"queued_consolidations"`
	QueuedTabCount           uint64                                       `json:"queued_tab_count"`
	TabView                      string                                       `json:"tab_view"`
}

type ConsolidationsPageDataRecentConsolidation struct {
	IsIncluded           bool      `json:"is_included"`
	SlotNumber           uint64    `json:"slot"`
	SlotRoot             []byte    `json:"slot_root"`
	Time                 time.Time `json:"time"`
	Status               uint64    `json:"status"`
	Result               uint8     `json:"result"`
	ResultMessage        string    `json:"result_message"`
	TxStatus             uint64    `json:"tx_status"`
	SourceAddr           []byte    `json:"src_addr"`
	SourceValidatorValid bool      `json:"src_vvalid"`
	SourceValidatorIndex uint64    `json:"src_vindex"`
	SourceValidatorName  string    `json:"src_vname"`
	SourcePublicKey      []byte    `json:"src_pubkey"`
	TargetValidatorValid bool      `json:"tgt_vvalid"`
	TargetValidatorIndex uint64    `json:"tgt_vindex"`
	TargetValidatorName  string    `json:"tgt_vname"`
	TargetPublicKey      []byte    `json:"tgt_pubkey"`
	LinkedTransaction    bool      `json:"linked_tx"`
	TransactionHash      []byte    `json:"tx_hash"`
}

type ConsolidationsPageDataQueuedConsolidation struct {
	SourcePublicKey        []byte    `json:"src_pubkey"`
	TargetPublicKey        []byte    `json:"tgt_pubkey"`
	SourceValidatorExists  bool      `json:"src_validator_exists"`
	SourceValidatorIndex   uint64    `json:"src_validator_index"`
	SourceValidatorName    string    `json:"src_validator_name"`
	SourceValidatorStatus  string    `json:"src_validator_status"`
	SourceEffectiveBalance uint64    `json:"src_effective_balance"`
	TargetValidatorExists  bool      `json:"tgt_validator_exists"`
	TargetValidatorIndex   uint64    `json:"tgt_validator_index"`
	TargetValidatorName    string    `json:"tgt_validator_name"`
	EstimatedTime          time.Time `json:"estimated_time"`
	ShowUpcheck            bool      `json:"show_upcheck"`
	UpcheckActivity        uint8     `json:"upcheck_act"`
	UpcheckMaximum         uint8     `json:"upcheck_max"`
}