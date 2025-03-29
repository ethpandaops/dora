package models

import (
	"time"
)

// DepositsPageData is a struct to hold info for the deposits page
type DepositsPageData struct {
	InitiatedDeposits     []*DepositsPageDataInitiatedDeposit `json:"initiated_deposits"`
	InitiatedDepositCount uint64                              `json:"initiated_deposit_count"`
	IncludedDeposits      []*DepositsPageDataIncludedDeposit  `json:"included_deposits"`
	IncludedDepositCount  uint64                              `json:"included_deposit_count"`
}

type DepositsPageDataInitiatedDeposit struct {
	Index                 uint64    `json:"index"`
	Address               []byte    `json:"address"`
	PublicKey             []byte    `json:"pubkey"`
	Withdrawalcredentials []byte    `json:"wtdcreds"`
	Amount                uint64    `json:"amount"`
	TxHash                []byte    `json:"txhash"`
	Time                  time.Time `json:"time"`
	Block                 uint64    `json:"block"`
	BlockHash             []byte    `json:"block_hash"`
	Orphaned              bool      `json:"orphaned"`
	Valid                 bool      `json:"valid"`
	ValidatorStatus       string    `json:"vstatus"`
	ShowUpcheck           bool      `json:"show_upcheck"`
	UpcheckActivity       uint8     `json:"upcheck_act"`
	UpcheckMaximum        uint8     `json:"upcheck_max"`
}

type DepositsPageDataIncludedDeposit struct {
	Index                 uint64                                    `json:"index"`
	HasIndex              bool                                      `json:"has_index"`
	PublicKey             []byte                                    `json:"pubkey"`
	Withdrawalcredentials []byte                                    `json:"wtdcreds"`
	Amount                uint64                                    `json:"amount"`
	SlotNumber            uint64                                    `json:"slot"`
	SlotRoot              []byte                                    `json:"slot_root"`
	Time                  time.Time                                 `json:"time"`
	Orphaned              bool                                      `json:"orphaned"`
	ValidatorStatus       string                                    `json:"vstatus"`
	ShowUpcheck           bool                                      `json:"show_upcheck"`
	UpcheckActivity       uint8                                     `json:"upcheck_act"`
	UpcheckMaximum        uint8                                     `json:"upcheck_max"`
	IsQueued              bool                                      `json:"is_queued"`
	QueuePosition         uint64                                    `json:"queue_position"`
	EstimatedTime         time.Time                                 `json:"estimated_time"`
	DepositorAddress      []byte                                    `json:"depositor_address"`
	HasTransaction        bool                                      `json:"has_transaction"`
	TransactionDetails    *DepositsPageDataIncludedDepositTxDetails `json:"tx_details"`
	ValidatorExists       bool                                      `json:"validator_exists"`
	ValidatorIndex        uint64                                    `json:"validator_index"`
	ValidatorName         string                                    `json:"validator_name"`
}

type DepositsPageDataIncludedDepositTxDetails struct {
	BlockNumber    uint64 `json:"block"`
	BlockHash      string `json:"block_hash"`
	BlockTime      uint64 `json:"block_time"`
	TxOrigin       string `json:"tx_origin"`
	TxTarget       string `json:"tx_target"`
	TxHash         string `json:"tx_hash"`
	ValidSignature bool   `json:"valid_signature"`
}
