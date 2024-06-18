package models

import (
	"time"
)

// IncludedDepositsPageData is a struct to hold info for the included_deposits page
type IncludedDepositsPageData struct {
	FilterMinIndex      uint64 `json:"filter_mini"`
	FilterMaxIndex      uint64 `json:"filter_maxi"`
	FilterPubKey        string `json:"filter_publickey"`
	FilterValidatorName string `json:"filter_vname"`
	FilterMinAmount     uint64 `json:"filter_mina"`
	FilterMaxAmount     uint64 `json:"filter_maxa"`
	FilterWithOrphaned  uint8  `json:"filter_orphaned"`

	Deposits     []*IncludedDepositsPageDataDeposit `json:"deposits"`
	DepositCount uint64                             `json:"deposit_count"`
	FirstIndex   uint64                             `json:"first_index"`
	LastIndex    uint64                             `json:"last_index"`

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
}

type IncludedDepositsPageDataDeposit struct {
	Index                 uint64    `json:"index"`
	HasIndex              bool      `json:"has_index"`
	PublicKey             []byte    `json:"pubkey"`
	Withdrawalcredentials []byte    `json:"wtdcreds"`
	Amount                uint64    `json:"amount"`
	SlotNumber            uint64    `json:"slot"`
	SlotRoot              []byte    `json:"slot_root"`
	Time                  time.Time `json:"time"`
	Orphaned              bool      `json:"orphaned"`
	ValidatorStatus       string    `json:"vstatus"`
	ShowUpcheck           bool      `json:"show_upcheck"`
	UpcheckActivity       uint8     `json:"upcheck_act"`
	UpcheckMaximum        uint8     `json:"upcheck_max"`
}
