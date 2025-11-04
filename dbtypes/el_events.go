package dbtypes

// ElEvent represents an event log from a transaction
type ElEvent struct {
	Id              uint64 `db:"id"`
	TransactionHash []byte `db:"transaction_hash"`
	BlockNumber     uint64 `db:"block_number"`
	BlockTimestamp  uint64 `db:"block_timestamp"`
	LogIndex        uint   `db:"log_index"`

	Address []byte  `db:"address"` // Contract that emitted the event
	Topic0  []byte  `db:"topic0"`  // Event signature
	Topic1  []byte  `db:"topic1"`
	Topic2  []byte  `db:"topic2"`
	Topic3  []byte  `db:"topic3"`
	Data    []byte  `db:"data"`

	EventName   *string `db:"event_name"`
	DecodedData *string `db:"decoded_data"` // JSON string for SQLite, JSONB for PostgreSQL
}

// ElEventFilter represents filter options for event queries
type ElEventFilter struct {
	TransactionHash []byte
	Address         []byte
	Topic0          []byte
	Topic1          []byte
	Topic2          []byte
	Topic3          []byte
	MinTimestamp    *uint64
	MaxTimestamp    *uint64
}

// ElMethodSignature represents a function signature for decoding
type ElMethodSignature struct {
	Signature     []byte `db:"signature"`      // 4-byte method ID
	Name          string `db:"name"`           // Human-readable name
	SignatureText string `db:"signature_text"` // Full signature e.g., "transfer(address,uint256)"
}

// ElEventSignature represents an event signature for decoding
type ElEventSignature struct {
	Signature     []byte `db:"signature"`      // 32-byte topic0 hash
	Name          string `db:"name"`           // Human-readable name
	SignatureText string `db:"signature_text"` // Full signature e.g., "Transfer(address,address,uint256)"
}
