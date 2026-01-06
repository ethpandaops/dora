package models

import (
	"time"
)

// TransactionPageData is a struct to hold info for the transaction page
type TransactionPageData struct {
	TxHash      []byte `json:"tx_hash"`
	TxNotFound  bool   `json:"tx_not_found"`
	TxMultiple  bool   `json:"tx_multiple"`  // If there are multiple versions (due to reorg)
	TxOrphaned  bool   `json:"tx_orphaned"`  // If the tx is from an orphaned block
	TxFinalized bool   `json:"tx_finalized"` // If the tx is in a finalized block

	// Status
	Status     bool   `json:"status"` // true = success, false = reverted
	StatusText string `json:"status_text"`

	// Block info
	BlockNumber uint64    `json:"block_number"`
	BlockHash   []byte    `json:"block_hash"`
	BlockRoot   []byte    `json:"block_root"` // Beacon block root for linking
	BlockTime   time.Time `json:"block_time"`
	Slot        uint64    `json:"slot"`

	// Addresses
	FromAddr       []byte `json:"from_addr"`
	FromIsContract bool   `json:"from_is_contract"`
	ToAddr         []byte `json:"to_addr"`
	ToIsContract   bool   `json:"to_is_contract"`
	HasTo          bool   `json:"has_to"`    // false for contract creation
	IsCreate       bool   `json:"is_create"` // Contract creation

	// Value and fees
	Amount      float64 `json:"amount"`
	AmountRaw   []byte  `json:"amount_raw"`
	TxFee       float64 `json:"tx_fee"`     // Gas used * effective gas price
	TxFeeRaw    []byte  `json:"tx_fee_raw"` // Raw fee in wei
	GasPrice    float64 `json:"gas_price"`  // Effective gas price in Gwei
	GasPriceRaw []byte  `json:"gas_price_raw"`
	TipPrice    float64 `json:"tip_price"` // Tip (priority fee) in Gwei

	// Gas
	GasLimit   uint64  `json:"gas_limit"`
	GasUsed    uint64  `json:"gas_used"`
	GasUsedPct float64 `json:"gas_used_pct"` // Gas used percentage

	// EIP-1559+ fields
	MaxFee float64 `json:"max_fee"` // Max fee per gas in Gwei

	// Transaction details
	TxType     uint8  `json:"tx_type"`
	TxTypeName string `json:"tx_type_name"`
	Nonce      uint64 `json:"nonce"`
	TxIndex    uint32 `json:"tx_index"` // Position in block

	// Input data
	InputData  []byte `json:"input_data"`
	MethodID   []byte `json:"method_id"`
	MethodName string `json:"method_name"` // If known

	// Blobs (EIP-4844)
	BlobCount uint32 `json:"blob_count"`

	// Tab view
	TabView string `json:"tab_view"`

	// Events tab
	Events     []*TransactionPageDataEvent `json:"events"`
	EventCount uint64                      `json:"event_count"`

	// Token transfers tab
	TokenTransfers     []*TransactionPageDataTokenTransfer `json:"token_transfers"`
	TokenTransferCount uint64                              `json:"token_transfer_count"`
}

// TransactionPageDataEvent represents an event/log in the transaction
type TransactionPageDataEvent struct {
	EventIndex uint32 `json:"event_index"`

	// Source contract
	SourceAddr       []byte `json:"source_addr"`
	SourceIsContract bool   `json:"source_is_contract"`

	// Topics
	Topic0 []byte `json:"topic0"`
	Topic1 []byte `json:"topic1"`
	Topic2 []byte `json:"topic2"`
	Topic3 []byte `json:"topic3"`
	Topic4 []byte `json:"topic4"`

	// Data
	Data []byte `json:"data"`

	// Decoded (if known)
	EventName string `json:"event_name"`
}

// TransactionPageDataTokenTransfer represents a token transfer in the transaction
type TransactionPageDataTokenTransfer struct {
	TransferIndex uint32 `json:"transfer_index"`

	// Token info
	TokenID     uint64 `json:"token_id"`
	Contract    []byte `json:"contract"`
	TokenName   string `json:"token_name"`
	TokenSymbol string `json:"token_symbol"`
	Decimals    uint8  `json:"decimals"`
	TokenType   uint8  `json:"token_type"` // 1=ERC20, 2=ERC721, 3=ERC1155

	// From/To
	FromAddr       []byte `json:"from_addr"`
	FromIsContract bool   `json:"from_is_contract"`
	ToAddr         []byte `json:"to_addr"`
	ToIsContract   bool   `json:"to_is_contract"`

	// Amount
	Amount    float64 `json:"amount"`
	AmountRaw []byte  `json:"amount_raw"`

	// NFT token index
	TokenIndex []byte `json:"token_index"`
}
