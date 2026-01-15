package models

import (
	"time"
)

// BuildersPageData is a struct to hold info for the builders page
type BuildersPageData struct {
	FilterPubKey        string                         `json:"filter_pubkey"`
	FilterIndex         string                         `json:"filter_index"`
	FilterExecutionAddr string                         `json:"filter_execution_addr"`
	FilterStatus        string                         `json:"filter_status"`
	FilterStatusOpts    []BuildersPageDataStatusOption `json:"filter_status_opts"`

	Builders         []*BuildersPageDataBuilder `json:"builders"`
	BuilderCount     uint64                     `json:"builder_count"`
	FirstBuilder     uint64                     `json:"first_builder"`
	LastBuilder      uint64                     `json:"last_builder"`
	Sorting          string                     `json:"sorting"`
	IsDefaultSorting bool                       `json:"default_sorting"`
	IsDefaultPage    bool                       `json:"default_page"`
	TotalPages       uint64                     `json:"total_pages"`
	PageSize         uint64                     `json:"page_size"`
	CurrentPageIndex uint64                     `json:"page_index"`
	PrevPageIndex    uint64                     `json:"prev_page_index"`
	NextPageIndex    uint64                     `json:"next_page_index"`
	LastPageIndex    uint64                     `json:"last_page_index"`
	FilteredPageLink string                     `json:"filtered_page_link"`

	UrlParams map[string]string `json:"url_params"`
}

type BuildersPageDataStatusOption struct {
	Status string `json:"status"`
	Count  uint64 `json:"count"`
}

type BuildersPageDataBuilder struct {
	Index             uint64    `json:"index"`
	PublicKey         []byte    `json:"pubkey"`
	ExecutionAddress  []byte    `json:"execution_address"`
	Balance           uint64    `json:"balance"`
	State             string    `json:"state"`
	ShowDeposit       bool      `json:"show_deposit"`
	DepositTs         time.Time `json:"deposit_ts"`
	DepositEpoch      uint64    `json:"deposit_epoch"`
	ShowWithdrawable  bool      `json:"show_withdrawable"`
	WithdrawableTs    time.Time `json:"withdrawable_ts"`
	WithdrawableEpoch uint64    `json:"withdrawable_epoch"`
}
