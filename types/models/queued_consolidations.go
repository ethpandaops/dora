package models

import (
	"time"
)

// QueuedConsolidationsPageData is a struct to hold info for the queued_consolidations page
type QueuedConsolidationsPageData struct {
	FilterMinSrcIndex   uint64 `json:"filter_minsi"`
	FilterMaxSrcIndex   uint64 `json:"filter_maxsi"`
	FilterMinTgtIndex   uint64 `json:"filter_minti"`
	FilterMaxTgtIndex   uint64 `json:"filter_maxti"`
	FilterValidatorName string `json:"filter_vname"`
	FilterPublicKey     string `json:"filter_pubkey"`

	QueuedConsolidations []*QueuedConsolidationsPageDataConsolidation `json:"consolidations"`
	ConsolidationCount   uint64                                       `json:"consolidation_count"`
	FirstIndex           uint64                                       `json:"first_index"`
	LastIndex            uint64                                       `json:"last_index"`

	IsDefaultPage    bool   `json:"default_page"`
	TotalPages       uint64 `json:"total_pages"`
	PageSize         uint64 `json:"page_size"`
	CurrentPageIndex uint64 `json:"page_index"`
	PrevPageIndex    uint64 `json:"prev_page_index"`
	NextPageIndex    uint64 `json:"next_page_index"`
	LastPageIndex    uint64 `json:"last_page_index"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`

	UrlParams map[string]string `json:"url_params"`
}

type QueuedConsolidationsPageDataConsolidation struct {
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
