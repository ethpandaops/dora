package models

import (
	"time"
)

// QueuedWithdrawalsPageData is a struct to hold info for the queued_withdrawals page
type QueuedWithdrawalsPageData struct {
	FilterMinIndex      uint64 `json:"filter_mini"`
	FilterMaxIndex      uint64 `json:"filter_maxi"`
	FilterValidatorName string `json:"filter_vname"`
	FilterPublicKey     string `json:"filter_pubkey"`

	QueuedWithdrawals []*QueuedWithdrawalsPageDataWithdrawal `json:"withdrawals"`
	WithdrawalCount   uint64                                 `json:"withdrawal_count"`
	FirstIndex        uint64                                 `json:"first_index"`
	LastIndex         uint64                                 `json:"last_index"`

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

type QueuedWithdrawalsPageDataWithdrawal struct {
	ValidatorIndex    uint64    `json:"validator_index"`
	ValidatorName     string    `json:"validator_name"`
	ValidatorStatus   string    `json:"validator_status"`
	PublicKey         []byte    `json:"pubkey"`
	Amount            uint64    `json:"amount"`
	WithdrawableEpoch uint64    `json:"withdrawable_epoch"`
	EstimatedTime     time.Time `json:"estimated_time"`
	ShowUpcheck       bool      `json:"show_upcheck"`
	UpcheckActivity   uint8     `json:"upcheck_act"`
	UpcheckMaximum    uint8     `json:"upcheck_max"`
}
