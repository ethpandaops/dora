package models

import (
	"time"
)

// QueuedDepositsPageData is a struct to hold info for the queued_deposits page
type QueuedDepositsPageData struct {
	Deposits            []*QueuedDepositsPageDataDeposit `json:"deposits"`
	DepositCount        uint64                           `json:"deposit_count"`
	DepositsFrom        uint64                           `json:"deposits_from"`
	DepositsTo          uint64                           `json:"deposits_to"`
	MaxEffectiveBalance uint64                           `json:"max_effective_balance"`

	// Filter fields
	FilterMinIndex  uint64 `json:"filter_mini"`
	FilterMaxIndex  uint64 `json:"filter_maxi"`
	FilterPubKey    string `json:"filter_publickey"`
	FilterMinAmount uint64 `json:"filter_mina"`
	FilterMaxAmount uint64 `json:"filter_maxa"`

	// Add paging fields
	IsDefaultPage    bool   `json:"default_page"`
	TotalPages       uint64 `json:"total_pages"`
	PageSize         uint64 `json:"page_size"`
	CurrentPageIndex uint64 `json:"page_index"`
	PrevPageIndex    uint64 `json:"prev_page_index"`
	NextPageIndex    uint64 `json:"next_page_index"`
	LastPageIndex    uint64 `json:"last_page_index"`
	FirstPageLink    string `json:"first_page_link"`
	PrevPageLink     string `json:"prev_page_link"`
	NextPageLink     string `json:"next_page_link"`
	LastPageLink     string `json:"last_page_link"`
}

type QueuedDepositsPageDataDeposit struct {
	Index                 uint64                                  `json:"index"`
	HasIndex              bool                                    `json:"has_index"`
	ExcessDeposit         bool                                    `json:"excess_deposit"`
	PublicKey             []byte                                  `json:"pubkey"`
	Withdrawalcredentials []byte                                  `json:"wtdcreds"`
	Amount                uint64                                  `json:"amount"`
	QueuePosition         uint64                                  `json:"queue_position"`
	EstimatedTime         time.Time                               `json:"estimated_time"`
	ValidatorStatus       string                                  `json:"vstatus"`
	ShowUpcheck           bool                                    `json:"show_upcheck"`
	UpcheckActivity       uint8                                   `json:"upcheck_act"`
	UpcheckMaximum        uint8                                   `json:"upcheck_max"`
	HasTransaction        bool                                    `json:"has_transaction"`
	TransactionHash       []byte                                  `json:"tx_hash"`
	TransactionDetails    *QueuedDepositsPageDataDepositTxDetails `json:"tx_details"`
	ValidatorExists       bool                                    `json:"validator_exists"`
	ValidatorIndex        uint64                                  `json:"validator_index"`
	ValidatorName         string                                  `json:"validator_name"`
}

type QueuedDepositsPageDataDepositTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
