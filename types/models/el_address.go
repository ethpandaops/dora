package models

import (
	"time"
)

// ElAddressPageData represents data for the EL address detail page
type ElAddressPageData struct {
	// Address info
	Address     string `json:"address"`
	EnsName     string `json:"ens_name,omitempty"`
	CustomName  string `json:"custom_name,omitempty"`
	IsContract  bool   `json:"is_contract"`

	// Contract info
	ContractCreator     string    `json:"contract_creator,omitempty"`
	ContractCreatorName string    `json:"contract_creator_name,omitempty"`
	CreationTxHash      string    `json:"creation_tx_hash,omitempty"`
	CreationTimestamp   time.Time `json:"creation_timestamp,omitempty"`
	ContractVerified    bool      `json:"contract_verified"`

	// Balance
	Balance       string `json:"balance"`        // Wei
	BalanceEth    string `json:"balance_eth"`    // Formatted ETH
	BalanceUsd    string `json:"balance_usd,omitempty"` // Future: with price feed

	// Token holdings
	TokenHoldingsCount uint                  `json:"token_holdings_count"`
	TokenHoldings      []*ElTokenBalance     `json:"token_holdings,omitempty"`
	TotalTokenValueUsd string                `json:"total_token_value_usd,omitempty"` // Future

	// Transaction stats
	FirstSeen      time.Time `json:"first_seen"`
	FirstSeenAge   string    `json:"first_seen_age"`
	LastSeen       time.Time `json:"last_seen"`
	LastSeenAge    string    `json:"last_seen_age"`
	TxCount        uint64    `json:"tx_count"`
	InTxCount      uint64    `json:"in_tx_count"`
	OutTxCount     uint64    `json:"out_tx_count"`

	// Tabs data
	CurrentTab        string                      `json:"current_tab"` // "transactions", "internal", "erc20", "erc721"
	Transactions      []*ElAddressTransaction     `json:"transactions,omitempty"`
	InternalTxs       []*ElAddressInternalTx      `json:"internal_txs,omitempty"`
	TokenTransfers    []*ElAddressTokenTransfer   `json:"token_transfers,omitempty"`

	// Pagination
	TotalCount  uint64 `json:"total_count"`
	PageSize    uint   `json:"page_size"`
	CurrentPage uint   `json:"current_page"`
	TotalPages  uint   `json:"total_pages"`
}

// ElTokenBalance represents a token balance for the address
type ElTokenBalance struct {
	TokenAddress   string    `json:"token_address"`
	TokenName      string    `json:"token_name,omitempty"`
	TokenSymbol    string    `json:"token_symbol,omitempty"`
	TokenType      string    `json:"token_type"`
	Decimals       uint8     `json:"decimals,omitempty"`
	Balance        string    `json:"balance"`           // Raw balance
	BalanceFormatted string  `json:"balance_formatted"` // With decimals applied
	ValueUsd       string    `json:"value_usd,omitempty"` // Future: with price feed
	UpdatedAt      time.Time `json:"updated_at"`
}

// ElAddressTransaction represents a transaction involving the address
type ElAddressTransaction struct {
	Hash             string    `json:"hash"`
	BlockNumber      uint64    `json:"block_number"`
	Timestamp        time.Time `json:"timestamp"`
	Age              string    `json:"age"`
	From             string    `json:"from"`
	FromName         string    `json:"from_name,omitempty"`
	To               string    `json:"to,omitempty"`
	ToName           string    `json:"to_name,omitempty"`
	Direction        string    `json:"direction"` // "IN", "OUT", "SELF"
	ValueEth         string    `json:"value_eth"`
	Status           uint8     `json:"status"`
	MethodName       string    `json:"method_name,omitempty"`
	GasUsed          uint64    `json:"gas_used"`
	EffectiveFeeGwei string    `json:"effective_fee_gwei,omitempty"`
}

// ElAddressInternalTx represents an internal transaction involving the address
type ElAddressInternalTx struct {
	TransactionHash string    `json:"transaction_hash"`
	BlockNumber     uint64    `json:"block_number"`
	Timestamp       time.Time `json:"timestamp"`
	Age             string    `json:"age"`
	From            string    `json:"from"`
	FromName        string    `json:"from_name,omitempty"`
	To              string    `json:"to,omitempty"`
	ToName          string    `json:"to_name,omitempty"`
	Direction       string    `json:"direction"` // "IN", "OUT"
	Type            string    `json:"type"`
	ValueEth        string    `json:"value_eth"`
}

// ElAddressTokenTransfer represents a token transfer involving the address
type ElAddressTokenTransfer struct {
	TransactionHash  string    `json:"transaction_hash"`
	BlockNumber      uint64    `json:"block_number"`
	Timestamp        time.Time `json:"timestamp"`
	Age              string    `json:"age"`
	TokenAddress     string    `json:"token_address"`
	TokenName        string    `json:"token_name,omitempty"`
	TokenSymbol      string    `json:"token_symbol,omitempty"`
	TokenType        string    `json:"token_type"`
	From             string    `json:"from"`
	FromName         string    `json:"from_name,omitempty"`
	To               string    `json:"to"`
	ToName           string    `json:"to_name,omitempty"`
	Direction        string    `json:"direction"` // "IN", "OUT"
	Value            string    `json:"value,omitempty"`
	ValueFormatted   string    `json:"value_formatted,omitempty"`
	TokenId          string    `json:"token_id,omitempty"`
}

// ElAddressesPageData represents data for the addresses list page
type ElAddressesPageData struct {
	Addresses   []*ElAddressSummary `json:"addresses"`
	TotalCount  uint64              `json:"total_count"`
	PageSize    uint                `json:"page_size"`
	CurrentPage uint                `json:"current_page"`
	TotalPages  uint                `json:"total_pages"`
}

// ElAddressSummary represents an address in the list
type ElAddressSummary struct {
	Address     string `json:"address"`
	EnsName     string `json:"ens_name,omitempty"`
	CustomName  string `json:"custom_name,omitempty"`
	IsContract  bool   `json:"is_contract"`
	BalanceEth  string `json:"balance_eth"`
	TxCount     uint64 `json:"tx_count"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}
