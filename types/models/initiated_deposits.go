package models

import (
	"time"
)

// DepositsPageData is a struct to hold info for the deposits page
type InitiatedDepositsPageData struct {
	FilterAddress       string `json:"filter_address"`
	FilterPubKey        string `json:"filter_publickey"`
	FilterValidatorName string `json:"filter_vname"`
	FilterMinAmount     uint64 `json:"filter_mina"`
	FilterMaxAmount     uint64 `json:"filter_maxa"`
	FilterWithOrphaned  uint8  `json:"filter_orphaned"`
	FilterWithValid     uint8  `json:"filter_valid"`

	Deposits     []*InitiatedDepositsPageDataDeposit `json:"deposits"`
	DepositCount uint64                              `json:"deposit_count"`
	FirstIndex   uint64                              `json:"first_index"`
	LastIndex    uint64                              `json:"last_index"`

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

type InitiatedDepositsPageDataDeposit struct {
	Index                 uint64    `json:"index"`
	Address               []byte    `json:"address"`
	PublicKey             []byte    `json:"pubkey"`
	Withdrawalcredentials []byte    `json:"wtdcreds"`
	Amount                uint64    `json:"amount"`
	TxHash                []byte    `json:"txhash"`
	Time                  time.Time `json:"time"`
	Block                 uint64    `json:"block"`
	Orphaned              bool      `json:"orphaned"`
	Valid                 bool      `json:"valid"`
	ValidatorStatus       string    `json:"vstatus"`
	ShowUpcheck           bool      `json:"show_upcheck"`
	UpcheckActivity       uint8     `json:"upcheck_act"`
	UpcheckMaximum        uint8     `json:"upcheck_max"`
	ValidatorExists       bool      `json:"validator_exists"`
	ValidatorIndex        uint64    `json:"validator_index"`
	ValidatorName         string    `json:"validator_name"`
	IsBuilder             bool      `json:"is_builder"`
}
