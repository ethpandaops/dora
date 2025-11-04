package dbtypes

// ElBlock represents an execution layer block
type ElBlock struct {
	Number       uint64 `db:"number"`
	Hash         []byte `db:"hash"`
	ParentHash   []byte `db:"parent_hash"`
	Timestamp    uint64 `db:"timestamp"`
	FeeRecipient []byte `db:"fee_recipient"`
	StateRoot    []byte `db:"state_root"`
	ReceiptsRoot []byte `db:"receipts_root"`
	LogsBloom    []byte `db:"logs_bloom"`

	Difficulty      []byte  `db:"difficulty"`
	TotalDifficulty []byte  `db:"total_difficulty"`
	Size            uint64  `db:"size"`
	GasUsed         uint64  `db:"gas_used"`
	GasLimit        uint64  `db:"gas_limit"`
	BaseFeePerGas   *uint64 `db:"base_fee_per_gas"`
	ExtraData       []byte  `db:"extra_data"`
	Nonce           []byte  `db:"nonce"`

	TransactionCount uint `db:"transaction_count"`
	InternalTxCount  uint `db:"internal_tx_count"`
	EventCount       uint `db:"event_count"`
	WithdrawalCount  uint `db:"withdrawal_count"`

	TotalFees []byte `db:"total_fees"`
	BurntFees []byte `db:"burnt_fees"`

	Orphaned bool   `db:"orphaned"`
	ForkId   uint64 `db:"fork_id"`
}

// ElBlockFilter represents filter options for block queries
type ElBlockFilter struct {
	MinNumber    *uint64
	MaxNumber    *uint64
	MinTimestamp *uint64
	MaxTimestamp *uint64
	FeeRecipient []byte
	MinGasUsed   *uint64
	WithOrphaned uint8 // 0=all, 1=only orphaned, 2=only canonical
}
