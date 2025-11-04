package dbtypes

// ElInternalTx represents an internal transaction (trace)
type ElInternalTx struct {
	Id              uint64 `db:"id"`
	TransactionHash []byte `db:"transaction_hash"`
	BlockNumber     uint64 `db:"block_number"`
	BlockTimestamp  uint64 `db:"block_timestamp"`

	TraceAddress string  `db:"trace_address"` // e.g., "0.1.2"
	TraceType    string  `db:"trace_type"`    // "call", "create", "suicide", "reward"
	CallType     *string `db:"call_type"`     // "call", "delegatecall", "staticcall", "callcode"

	FromAddress []byte  `db:"from_address"`
	ToAddress   []byte  `db:"to_address"`
	Value       []byte  `db:"value"`
	Gas         *uint64 `db:"gas"`
	GasUsed     *uint64 `db:"gas_used"`
	InputData   []byte  `db:"input_data"`
	OutputData  []byte  `db:"output_data"`

	Error           *string `db:"error"`
	CreatedContract []byte  `db:"created_contract"`
}

// ElInternalTxFilter represents filter options for internal transaction queries
type ElInternalTxFilter struct {
	TransactionHash []byte
	FromAddress     []byte
	ToAddress       []byte
	TraceType       string
	MinValue        []byte
	WithError       *bool
}
