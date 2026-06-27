package types

import "context"

// TxHashPrefixLen is the number of leading tx-hash bytes stored in the index.
// 10 bytes (80 bits) keeps collisions negligible (~1e-8 across ~160M txs) while
// using only ~1/3 of the full 32-byte hash. Candidates are always disambiguated
// against the full hash, so a collision is harmless.
const TxHashPrefixLen = 10

// TxHashEntry maps a tx-hash prefix to its tx_uid for index insertion.
type TxHashEntry struct {
	Prefix []byte // TxHashPrefixLen bytes
	TxUid  uint64 // slot<<32 | block_index<<16 | tx_index
}

// TxHashIndex is an optional blockdb-layer capability that maps tx-hash prefixes
// to tx_uids so a transaction can be located by hash after its relational row
// has been pruned. It is implemented natively by the Pebble/Tiered engines and
// by a relational adapter for the S3 engine.
type TxHashIndex interface {
	// PutTxHashes inserts (idempotently) one entry per transaction.
	PutTxHashes(ctx context.Context, entries []TxHashEntry) error
	// LookupTxHash returns all candidate tx_uids for an exact prefix.
	LookupTxHash(ctx context.Context, prefix []byte) ([]uint64, error)
	// LookupTxHashRange returns candidate tx_uids for prefixes in [lo, hi),
	// used for partial-hash search.
	LookupTxHashRange(ctx context.Context, lo, hi []byte) ([]uint64, error)
	// PruneTxHashBefore removes all entries for slots below maxSlot and returns
	// the number of entries removed.
	PruneTxHashBefore(ctx context.Context, maxSlot uint64) (int64, error)
}

// HashPrefix returns a fresh TxHashPrefixLen-byte copy of the tx hash (never
// aliases the input). A short input is right-padded with zeroes.
func HashPrefix(txHash []byte) []byte {
	out := make([]byte, TxHashPrefixLen)
	copy(out, txHash)
	return out
}
