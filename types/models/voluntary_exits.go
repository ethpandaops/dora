package models

import (
	"time"
)

// VoluntaryExitsPageData is a struct to hold info for the voluntary_exits page
type VoluntaryExitsPageData struct {
	FilterMinSlot       uint64 `json:"filter_mins"`
	FilterMaxSlot       uint64 `json:"filter_maxs"`
	FilterMinIndex      uint64 `json:"filter_mini"`
	FilterMaxIndex      uint64 `json:"filter_maxi"`
	FilterValidatorName string `json:"filter_vname"`
	FilterWithOrphaned  uint8  `json:"filter_orphaned"`

	VoluntaryExits []*VoluntaryExitsPageDataExit `json:"exits"`
	ExitCount      uint64                        `json:"exit_count"`
	FirstIndex     uint64                        `json:"first_index"`
	LastIndex      uint64                        `json:"last_index"`

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

type VoluntaryExitsPageDataExit struct {
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
