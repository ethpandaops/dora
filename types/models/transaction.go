package models

import (
	"time"
)

// TransactionViewMode represents the data availability mode for a transaction
type TransactionViewMode uint8

const (
	// TxViewModeFull - full data available (from DB or tx+receipt from EL)
	TxViewModeFull TransactionViewMode = iota
	// TxViewModePartial - only tx data available (no receipt)
	TxViewModePartial
	// TxViewModeNone - transaction not found
	TxViewModeNone
)

// TransactionPageDataBlock represents a block that includes the transaction
type TransactionPageDataBlock struct {
	BlockUid    uint64    `json:"block_uid"`
	BlockNumber uint64    `json:"block_number"`
	BlockHash   []byte    `json:"block_hash"`
	BlockRoot   []byte    `json:"block_root"`
	Slot        uint64    `json:"slot"`
	BlockTime   time.Time `json:"block_time"`
	IsOrphaned  bool      `json:"is_orphaned"`
	IsCanonical bool      `json:"is_canonical"`
	IsSelected  bool      `json:"is_selected"` // Currently selected for viewing
	TxIndex     uint32    `json:"tx_index"`
}

// TransactionPageData is a struct to hold info for the transaction page
type TransactionPageData struct {
	TxHash      []byte `json:"tx_hash"`
	TxNotFound  bool   `json:"tx_not_found"`
	TxMultiple  bool   `json:"tx_multiple"`  // If there are multiple versions (due to reorg)
	TxOrphaned  bool   `json:"tx_orphaned"`  // If the tx is from an orphaned block
	TxFinalized bool   `json:"tx_finalized"` // If the tx is in a finalized block

	// View mode
	ViewMode   TransactionViewMode `json:"view_mode"`   // full/partial/none
	HasReceipt bool                `json:"has_receipt"` // Whether receipt data is available

	// Status (only available with receipt)
	Status     bool   `json:"status"` // true = success, false = reverted
	StatusText string `json:"status_text"`

	// Block info (primary/canonical block)
	BlockNumber uint64    `json:"block_number"`
	BlockHash   []byte    `json:"block_hash"`
	BlockRoot   []byte    `json:"block_root"` // Beacon block root for linking
	BlockTime   time.Time `json:"block_time"`
	Slot        uint64    `json:"slot"`

	// All blocks that include this transaction
	InclusionBlocks  []*TransactionPageDataBlock `json:"inclusion_blocks"`
	SelectedBlockUid uint64                      `json:"selected_block_uid"` // Currently selected block UID (0 = canonical)

	// Addresses
	FromAddr       []byte `json:"from_addr"`
	FromIsContract bool   `json:"from_is_contract"`
	ToAddr         []byte `json:"to_addr"`
	ToIsContract   bool   `json:"to_is_contract"`
	HasTo          bool   `json:"has_to"`    // false for contract creation
	IsCreate       bool   `json:"is_create"` // Contract creation

	// Value and fees
	Amount        float64 `json:"amount"`
	AmountRaw     []byte  `json:"amount_raw"`
	TxFee         float64 `json:"tx_fee"`     // Gas used * effective gas price
	TxFeeRaw      []byte  `json:"tx_fee_raw"` // Raw fee in wei
	GasPrice      float64 `json:"gas_price"`  // Legacy: gas price; EIP-1559+: maxFeePerGas (in Gwei)
	GasPriceRaw   []byte  `json:"gas_price_raw"`
	TipPrice      float64 `json:"tip_price"`       // Tip (priority fee) in Gwei
	EffGasPrice   float64 `json:"eff_gas_price"`   // Effective gas price actually paid (in Gwei)
	FeeSavingsPct float64 `json:"fee_savings_pct"` // Percentage saved (maxFee - effGasPrice) / maxFee * 100

	// Gas
	GasLimit   uint64  `json:"gas_limit"`
	GasUsed    uint64  `json:"gas_used"`
	GasUsedPct float64 `json:"gas_used_pct"` // Gas used percentage

	// Transaction details
	TxType     uint8  `json:"tx_type"`
	TxTypeName string `json:"tx_type_name"`
	Nonce      uint64 `json:"nonce"`
	TxIndex    uint32 `json:"tx_index"` // Position in block

	// Input data
	InputData  []byte `json:"input_data"`
	MethodID   []byte `json:"method_id"`
	MethodName string `json:"method_name"` // If known

	// Full transaction data (loaded from beacon block)
	TxRLP  string `json:"tx_rlp"`  // Hex-encoded RLP for copy button
	TxJSON string `json:"tx_json"` // JSON representation for copy button

	// Blobs (EIP-4844)
	BlobCount      uint32                     `json:"blob_count"`
	BlobGasLimit   uint64                     `json:"blob_gas_limit"`   // Max blob gas (131072 * blob_count)
	BlobGasUsed    uint64                     `json:"blob_gas_used"`    // Actual blob gas used
	BlobGasPrice   float64                    `json:"blob_gas_price"`   // Blob gas price in Gwei
	BlobGasFeeCap  float64                    `json:"blob_gas_fee_cap"` // Max blob fee per gas in Gwei
	BlobFee        float64                    `json:"blob_fee"`         // Total blob fee paid (blob_gas_used * blob_gas_price) in ETH
	BlobFeeRaw     []byte                     `json:"blob_fee_raw"`     // Raw blob fee in wei
	BlobFeeSavings float64                    `json:"blob_fee_savings"` // Savings percentage ((fee_cap - price) / fee_cap * 100)
	Blobs          []*TransactionPageDataBlob `json:"blobs"`            // Blob details

	// Tab view
	TabView string `json:"tab_view"`

	// Events tab
	Events     []*TransactionPageDataEvent `json:"events"`
	EventCount uint64                      `json:"event_count"`

	// Token transfers tab
	TokenTransfers     []*TransactionPageDataTokenTransfer `json:"token_transfers"`
	TokenTransferCount uint64                              `json:"token_transfer_count"`

	// Internal transactions tab
	InternalTxs             []*TransactionPageDataInternalTx `json:"internal_txs"`
	InternalTxCount         uint64                           `json:"internal_tx_count"`
	DataStatus              uint16                           `json:"data_status"` // blockdb data availability flags
	EventsNotAvailable      bool                             `json:"events_not_available"`
	InternalTxsNotAvailable bool                             `json:"internal_txs_not_available"`
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

// TransactionPageDataInternalTx represents an internal transaction (call trace frame)
type TransactionPageDataInternalTx struct {
	CallIndex uint32 `json:"call_index"`
	Depth     uint16 `json:"depth"`
	CallType  uint8  `json:"call_type"`
	TypeName  string `json:"type_name"`

	// From/To
	FromAddr       []byte `json:"from_addr"`
	FromIsContract bool   `json:"from_is_contract"`
	ToAddr         []byte `json:"to_addr"`
	ToIsContract   bool   `json:"to_is_contract"`

	// Value
	Amount    float64 `json:"amount"`
	AmountRaw []byte  `json:"amount_raw"`

	// Gas
	Gas     uint64 `json:"gas"`
	GasUsed uint64 `json:"gas_used"`

	// Status
	Status    uint8  `json:"status"` // 0=success, 1=reverted, 2=error
	ErrorText string `json:"error_text"`

	// Input/Output (from blockdb call trace, empty for DB-only fallback)
	Input      []byte `json:"input"`
	Output     []byte `json:"output"`
	MethodID   []byte `json:"method_id"`
	MethodName string `json:"method_name"`

	// Whether this entry has full trace data (from blockdb)
	HasTraceData bool `json:"has_trace_data"`
}

// TransactionPageDataBlob represents a blob in a blob transaction
type TransactionPageDataBlob struct {
	Index         uint64 `json:"index"`          // Blob index in transaction
	VersionedHash []byte `json:"versioned_hash"` // Versioned hash from transaction
	KzgCommitment []byte `json:"kzg_commitment"` // KZG commitment from beacon block
	KzgProof      []byte `json:"kzg_proof"`      // KZG proof (if available)
	HaveData      bool   `json:"have_data"`      // Whether full blob data is available
	BlobShort     []byte `json:"blob_short"`     // First bytes of blob data (preview)
}
