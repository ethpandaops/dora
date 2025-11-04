package dbtypes

// ElTransaction represents an execution layer transaction
type ElTransaction struct {
	Hash             []byte `db:"hash"`
	BlockNumber      uint64 `db:"block_number"`
	BlockHash        []byte `db:"block_hash"`
	BlockTimestamp   uint64 `db:"block_timestamp"`
	TransactionIndex uint   `db:"transaction_index"`

	FromAddress []byte  `db:"from_address"`
	ToAddress   []byte  `db:"to_address"`
	Value       []byte  `db:"value"`
	Nonce       uint64  `db:"nonce"`

	GasLimit             uint64  `db:"gas_limit"`
	GasPrice             *uint64 `db:"gas_price"`
	MaxFeePerGas         *uint64 `db:"max_fee_per_gas"`
	MaxPriorityFeePerGas *uint64 `db:"max_priority_fee_per_gas"`
	EffectiveGasPrice    *uint64 `db:"effective_gas_price"`
	GasUsed              uint64  `db:"gas_used"`

	InputData       []byte  `db:"input_data"`
	MethodId        []byte  `db:"method_id"`
	TransactionType uint8   `db:"transaction_type"`

	Status       uint8   `db:"status"` // 1=success, 0=failed
	ErrorMessage *string `db:"error_message"`

	ContractAddress []byte `db:"contract_address"`
	LogsCount       uint   `db:"logs_count"`
	InternalTxCount uint   `db:"internal_tx_count"`

	Orphaned bool   `db:"orphaned"`
	ForkId   uint64 `db:"fork_id"`
}

// ElTransactionFilter represents filter options for transaction queries
type ElTransactionFilter struct {
	FromAddress  []byte
	ToAddress    []byte
	MethodId     []byte
	Status       *uint8
	MinValue     []byte
	MinTimestamp *uint64
	MaxTimestamp *uint64
	WithOrphaned uint8 // 0=all, 1=only orphaned, 2=only canonical
}
