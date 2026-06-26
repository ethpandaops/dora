package db

import (
	"context"
	"fmt"
	"strings"

	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertElTxHashes inserts tx-hash index entries within the caller's
// transaction (so the relational store stays atomic with el_transactions).
// Idempotent: duplicate (hash10, tx_uid) pairs are ignored.
func InsertElTxHashes(ctx context.Context, dbTx *sqlx.Tx, entries []dbtypes.ElTxHash) error {
	if len(entries) == 0 {
		return nil
	}

	var sql strings.Builder
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO el_txhash (hash10, tx_uid) VALUES ",
		dbtypes.DBEngineSqlite: "INSERT OR IGNORE INTO el_txhash (hash10, tx_uid) VALUES ",
	}))

	args := make([]any, len(entries)*2)
	for i, entry := range entries {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		fmt.Fprintf(&sql, "($%v, $%v)", i*2+1, i*2+2)
		args[i*2] = entry.Hash10
		args[i*2+1] = entry.TxUid
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  " ON CONFLICT (hash10, tx_uid) DO NOTHING",
		dbtypes.DBEngineSqlite: "",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	return err
}

// GetTxUidsByHashPrefix returns all candidate tx_uids for an exact 10-byte
// prefix. Multiple results indicate a prefix collision (disambiguate by full
// hash) or the same tx across reorged blocks.
func GetTxUidsByHashPrefix(ctx context.Context, prefix []byte) ([]uint64, error) {
	uids := []uint64{}
	err := ReaderDb.SelectContext(ctx, &uids, "SELECT tx_uid FROM el_txhash WHERE hash10 = $1", prefix)
	if err != nil {
		return nil, err
	}
	return uids, nil
}

// GetTxUidsByHashPrefixRange returns candidate tx_uids for prefixes in [lo, hi),
// used for partial-hash (search-ahead) lookups. Capped to a small result set.
func GetTxUidsByHashPrefixRange(ctx context.Context, lo, hi []byte) ([]uint64, error) {
	uids := []uint64{}
	err := ReaderDb.SelectContext(ctx, &uids,
		"SELECT tx_uid FROM el_txhash WHERE hash10 >= $1 AND hash10 < $2 ORDER BY hash10 LIMIT 50", lo, hi)
	if err != nil {
		return nil, err
	}
	return uids, nil
}

// PruneElTxHashBefore deletes index entries for tx_uids below the threshold
// (= maxSlot<<32), batched like the other EL cleanup deletes.
func PruneElTxHashBefore(ctx context.Context, txUidThreshold uint64) (int64, error) {
	return batchDeleteBefore(ctx, "el_txhash", "tx_uid", txUidThreshold, 50000)
}

// GetElTxHashStats returns an approximate entry count and the table's on-disk
// size (bytes) for the relational tx-hash index, for debug display. The count
// is an estimate on Postgres (planner statistics) to avoid a full scan; size is
// only available on Postgres.
func GetElTxHashStats(ctx context.Context) (count int64, sizeBytes int64, err error) {
	err = ReaderDb.GetContext(ctx, &count, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "SELECT reltuples::bigint FROM pg_class WHERE relname = 'el_txhash'",
		dbtypes.DBEngineSqlite: "SELECT COUNT(*) FROM el_txhash",
	}))
	if err != nil {
		return 0, 0, err
	}
	if count < 0 {
		count = 0 // pg reltuples is -1 before the first ANALYZE
	}

	if DbEngine == dbtypes.DBEnginePgsql {
		if serr := ReaderDb.GetContext(ctx, &sizeBytes, "SELECT pg_total_relation_size('el_txhash')"); serr != nil {
			sizeBytes = 0
		}
	}
	return count, sizeBytes, nil
}

// dbTxHashIndex is the relational implementation of bdbtypes.TxHashIndex, used
// when the blockdb engine has no native tx-hash index (s3) or when the operator
// forces postgres storage.
type dbTxHashIndex struct{}

// NewDBTxHashIndex returns a relational tx-hash index backed by el_txhash.
func NewDBTxHashIndex() bdbtypes.TxHashIndex {
	return &dbTxHashIndex{}
}

func (*dbTxHashIndex) PutTxHashes(ctx context.Context, entries []bdbtypes.TxHashEntry) error {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]dbtypes.ElTxHash, len(entries))
	for i, entry := range entries {
		rows[i] = dbtypes.ElTxHash{Hash10: entry.Prefix, TxUid: entry.TxUid}
	}
	return RunDBTransaction(func(tx *sqlx.Tx) error {
		return InsertElTxHashes(ctx, tx, rows)
	})
}

func (*dbTxHashIndex) LookupTxHash(ctx context.Context, prefix []byte) ([]uint64, error) {
	return GetTxUidsByHashPrefix(ctx, prefix)
}

func (*dbTxHashIndex) LookupTxHashRange(ctx context.Context, lo, hi []byte) ([]uint64, error) {
	return GetTxUidsByHashPrefixRange(ctx, lo, hi)
}

func (*dbTxHashIndex) PruneTxHashBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	return PruneElTxHashBefore(ctx, maxSlot<<32)
}
