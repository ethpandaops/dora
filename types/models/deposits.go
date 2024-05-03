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
}

type DepositsPageDataIncludedDeposit struct {
	Index                 uint64    `json:"index"`
	HasIndex              bool      `json:"has_index"`
	PublicKey             []byte    `json:"pubkey"`
	Withdrawalcredentials []byte    `json:"wtdcreds"`
	Amount                uint64    `json:"amount"`
	SlotNumber            uint64    `json:"slot"`
	SlotRoot              []byte    `json:"slot_root"`
	Time                  time.Time `json:"time"`
	Orphaned              bool      `json:"orphaned"`
}
