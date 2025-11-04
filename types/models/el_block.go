package models

import (
	"time"
)

// ElBlockPageData represents data for the EL block detail page
type ElBlockPageData struct {
	// Block identifiers
	Number       uint64 `json:"number"`
	Hash         string `json:"hash"`
	ParentHash   string `json:"parent_hash"`
	StateRoot    string `json:"state_root"`
	ReceiptsRoot string `json:"receipts_root"`

	// Timing
	Timestamp time.Time `json:"timestamp"`
	Age       string    `json:"age"` // Relative time like "2 mins ago"

	// Block producer
	FeeRecipient      string `json:"fee_recipient"`
	FeeRecipientName  string `json:"fee_recipient_name,omitempty"`
	BlockReward       string `json:"block_reward"`       // Calculated from fees
	BlockRewardEth    string `json:"block_reward_eth"`   // Formatted in ETH
	TotalDifficulty   string `json:"total_difficulty"`
	Difficulty        string `json:"difficulty"`

	// Size
	Size      uint64 `json:"size"`
	SizeKB    string `json:"size_kb"` // Formatted

	// Gas
	GasUsed        uint64  `json:"gas_used"`
	GasLimit       uint64  `json:"gas_limit"`
	GasUsedPercent float64 `json:"gas_used_percent"`
	BaseFeePerGas  *uint64 `json:"base_fee_per_gas,omitempty"`
	BaseFeeGwei    string  `json:"base_fee_gwei,omitempty"` // Formatted in Gwei
	BurntFees      string  `json:"burnt_fees,omitempty"`    // EIP-1559 burnt fees in ETH
	BurntFeesWei   string  `json:"burnt_fees_wei,omitempty"`

	// Transactions
	TransactionCount uint                         `json:"transaction_count"`
	Transactions     []*ElBlockTransactionSummary `json:"transactions"`

	// Metadata
	ExtraData string `json:"extra_data"`
	Nonce     string `json:"nonce"`
	Orphaned  bool   `json:"orphaned"`

	// Internal counts
	InternalTxCount uint `json:"internal_tx_count"`
	EventCount      uint `json:"event_count"`
}

// ElBlockTransactionSummary represents a transaction summary in the block list
type ElBlockTransactionSummary struct {
	Hash          string `json:"hash"`
	MethodName    string `json:"method_name,omitempty"`
	From          string `json:"from"`
	FromName      string `json:"from_name,omitempty"`
	To            string `json:"to,omitempty"`
	ToName        string `json:"to_name,omitempty"`
	ToIsContract  bool   `json:"to_is_contract"`
	Value         string `json:"value"`      // Wei
	ValueEth      string `json:"value_eth"`  // Formatted ETH
	Status        uint8  `json:"status"`     // 1=success, 0=failed
	GasUsed       uint64 `json:"gas_used"`
	EffectiveFee  string `json:"effective_fee,omitempty"` // In Gwei
}

// ElBlocksPageData represents data for the blocks list page
type ElBlocksPageData struct {
	Blocks      []*ElBlockSummary `json:"blocks"`
	TotalBlocks uint64            `json:"total_blocks"`
	PageSize    uint              `json:"page_size"`
	CurrentPage uint              `json:"current_page"`
	TotalPages  uint              `json:"total_pages"`
}

// ElBlockSummary represents a block in the blocks list
type ElBlockSummary struct {
	Number           uint64    `json:"number"`
	Hash             string    `json:"hash"`
	Timestamp        time.Time `json:"timestamp"`
	Age              string    `json:"age"`
	FeeRecipient     string    `json:"fee_recipient"`
	FeeRecipientName string    `json:"fee_recipient_name,omitempty"`
	TransactionCount uint      `json:"transaction_count"`
	GasUsed          uint64    `json:"gas_used"`
	GasLimit         uint64    `json:"gas_limit"`
	GasUsedPercent   float64   `json:"gas_used_percent"`
	BaseFeeGwei      string    `json:"base_fee_gwei,omitempty"`
	BlockRewardEth   string    `json:"block_reward_eth"`
}
