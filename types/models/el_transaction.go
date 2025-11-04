package models

import (
	"time"
)

// ElTransactionPageData represents data for the EL transaction detail page
type ElTransactionPageData struct {
	// Basic identifiers
	Hash             string    `json:"hash"`
	BlockNumber      uint64    `json:"block_number"`
	BlockHash        string    `json:"block_hash"`
	Timestamp        time.Time `json:"timestamp"`
	Age              string    `json:"age"`
	Status           uint8     `json:"status"` // 1=success, 0=failed
	StatusText       string    `json:"status_text"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	Confirmations    uint64    `json:"confirmations"`

	// Addresses
	From             string `json:"from"`
	FromName         string `json:"from_name,omitempty"`
	FromIsContract   bool   `json:"from_is_contract"`
	To               string `json:"to,omitempty"`
	ToName           string `json:"to_name,omitempty"`
	ToIsContract     bool   `json:"to_is_contract"`
	ContractAddress  string `json:"contract_address,omitempty"`
	ContractCreated  bool   `json:"contract_created"`

	// Value
	Value    string `json:"value"`     // Wei as string
	ValueEth string `json:"value_eth"` // Formatted ETH

	// Gas & Fees
	TransactionType      uint8   `json:"transaction_type"` // 0=legacy, 1=EIP-2930, 2=EIP-1559
	GasLimit             uint64  `json:"gas_limit"`
	GasUsed              uint64  `json:"gas_used"`
	GasUsedPercent       float64 `json:"gas_used_percent"`
	GasPrice             *uint64 `json:"gas_price,omitempty"`             // Legacy
	GasPriceGwei         string  `json:"gas_price_gwei,omitempty"`        // Formatted
	BaseFeePerGas        *uint64 `json:"base_fee_per_gas,omitempty"`      // EIP-1559
	BaseFeeGwei          string  `json:"base_fee_gwei,omitempty"`         // Formatted
	MaxFeePerGas         *uint64 `json:"max_fee_per_gas,omitempty"`       // EIP-1559
	MaxFeeGwei           string  `json:"max_fee_gwei,omitempty"`          // Formatted
	MaxPriorityFeePerGas *uint64 `json:"max_priority_fee_per_gas,omitempty"` // EIP-1559
	MaxPriorityGwei      string  `json:"max_priority_gwei,omitempty"`     // Formatted
	EffectiveGasPrice    *uint64 `json:"effective_gas_price,omitempty"`
	EffectiveGasPriceGwei string `json:"effective_gas_price_gwei,omitempty"`

	// Calculated fees
	TransactionFee    string `json:"transaction_fee"`     // Total fee in ETH
	TransactionFeeWei string `json:"transaction_fee_wei"` // Total fee in Wei
	BurntFees         string `json:"burnt_fees,omitempty"`     // EIP-1559 burnt fees in ETH
	BurntFeesWei      string `json:"burnt_fees_wei,omitempty"` // EIP-1559 burnt fees in Wei

	// Transaction metadata
	Nonce            uint64 `json:"nonce"`
	TransactionIndex uint   `json:"transaction_index"`
	PositionInBlock  string `json:"position_in_block"` // e.g., "12 of 150"

	// Input data
	InputData         string `json:"input_data"`
	MethodId          string `json:"method_id,omitempty"`
	MethodSignature   string `json:"method_signature,omitempty"`   // Decoded name
	DecodedInputData  string `json:"decoded_input_data,omitempty"` // Future: ABI decoded params

	// Logs & Internal Txs
	Logs             []*ElTransactionLog        `json:"logs,omitempty"`
	LogsCount        uint                       `json:"logs_count"`
	InternalTxs      []*ElInternalTxSummary     `json:"internal_txs,omitempty"`
	InternalTxCount  uint                       `json:"internal_tx_count"`
	TokenTransfers   []*ElTokenTransferSummary  `json:"token_transfers,omitempty"`
}

// ElTransactionLog represents a log/event from the transaction
type ElTransactionLog struct {
	Index       uint     `json:"index"`
	Address     string   `json:"address"`
	AddressName string   `json:"address_name,omitempty"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	EventName   string   `json:"event_name,omitempty"`
	DecodedData string   `json:"decoded_data,omitempty"` // Future: decoded event params
}

// ElInternalTxSummary represents an internal transaction
type ElInternalTxSummary struct {
	TraceAddress string `json:"trace_address"`
	Type         string `json:"type"`
	CallType     string `json:"call_type,omitempty"`
	From         string `json:"from"`
	FromName     string `json:"from_name,omitempty"`
	To           string `json:"to,omitempty"`
	ToName       string `json:"to_name,omitempty"`
	Value        string `json:"value"`
	ValueEth     string `json:"value_eth"`
	Gas          uint64 `json:"gas,omitempty"`
	GasUsed      uint64 `json:"gas_used,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ElTokenTransferSummary represents a token transfer
type ElTokenTransferSummary struct {
	TokenAddress string `json:"token_address"`
	TokenSymbol  string `json:"token_symbol,omitempty"`
	TokenName    string `json:"token_name,omitempty"`
	TokenType    string `json:"token_type"` // ERC20, ERC721, ERC1155
	From         string `json:"from"`
	FromName     string `json:"from_name,omitempty"`
	To           string `json:"to"`
	ToName       string `json:"to_name,omitempty"`
	Value        string `json:"value,omitempty"`        // For ERC20
	ValueFormatted string `json:"value_formatted,omitempty"` // With decimals
	TokenId      string `json:"token_id,omitempty"`     // For ERC721/1155
}

// ElTransactionsPageData represents data for the transactions list page
type ElTransactionsPageData struct {
	Transactions []*ElTransactionSummary `json:"transactions"`
	TotalCount   uint64                  `json:"total_count"`
	PageSize     uint                    `json:"page_size"`
	CurrentPage  uint                    `json:"current_page"`
	TotalPages   uint                    `json:"total_pages"`
}

// ElTransactionSummary represents a transaction in the list
type ElTransactionSummary struct {
	Hash             string    `json:"hash"`
	BlockNumber      uint64    `json:"block_number"`
	Timestamp        time.Time `json:"timestamp"`
	Age              string    `json:"age"`
	From             string    `json:"from"`
	FromName         string    `json:"from_name,omitempty"`
	To               string    `json:"to,omitempty"`
	ToName           string    `json:"to_name,omitempty"`
	ToIsContract     bool      `json:"to_is_contract"`
	ValueEth         string    `json:"value_eth"`
	Status           uint8     `json:"status"`
	MethodName       string    `json:"method_name,omitempty"`
	GasUsed          uint64    `json:"gas_used"`
	EffectiveFeeGwei string    `json:"effective_fee_gwei,omitempty"`
}
