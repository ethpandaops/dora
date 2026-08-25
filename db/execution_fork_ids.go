package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// executionRequestTxTables are the tables holding execution-layer system contract requests. Each
// row records the execution block it was seen in and the consensus fork that block belonged to at
// the time it was indexed.
//
// The rows are keyed by block_root, which for these tables is the *execution* block hash
// (log.BlockHash), not a consensus block root. That is the right key to re-tag by: an execution
// block hash identifies exactly one block on exactly one chain, where a block number does not.
var executionRequestTxTables = []string{
	"deposit_txs",
	"builder_deposit_request_txs",
	"builder_exit_request_txs",
	"withdrawal_request_txs",
	"consolidation_request_txs",
}

// updateForkIdBatchSize bounds how many block hashes go into one UPDATE, keeping the statement
// under the driver's bind-parameter limit (SQLite allows 32766, and older builds only 999).
const updateForkIdBatchSize = 900

// UpdateExecutionRequestTxsForkId re-tags the execution-layer request transactions of the given
// execution blocks onto a fork.
//
// The consensus fork a block belongs to is not final when the block is first indexed: a fork id
// handed out while the chain was still being resolved is superseded once the blocks are re-linked
// onto their real fork. Consensus rows follow that move because the blocks are re-persisted, but
// these are written once by the execution contract indexers and would otherwise keep the stale id
// forever — which makes canonical transactions read as orphaned and stops them pairing with the
// consensus requests they produced.
func UpdateExecutionRequestTxsForkId(ctx context.Context, tx *sqlx.Tx, executionBlockHashes [][]byte, forkId uint64) error {
	if len(executionBlockHashes) == 0 {
		return nil
	}

	for start := 0; start < len(executionBlockHashes); start += updateForkIdBatchSize {
		end := start + updateForkIdBatchSize
		if end > len(executionBlockHashes) {
			end = len(executionBlockHashes)
		}

		chunk := executionBlockHashes[start:end]

		for _, table := range executionRequestTxTables {
			var sql strings.Builder

			args := make([]any, 0, len(chunk)+1)
			args = append(args, forkId)

			fmt.Fprintf(&sql, "UPDATE %v SET fork_id = $1 WHERE block_root IN (", table)

			for i, blockHash := range chunk {
				if i > 0 {
					fmt.Fprint(&sql, ", ")
				}

				args = append(args, blockHash)
				fmt.Fprintf(&sql, "$%v", len(args))
			}

			fmt.Fprint(&sql, ")")

			if _, err := tx.ExecContext(ctx, sql.String(), args...); err != nil {
				return fmt.Errorf("error updating %v fork ids: %w", table, err)
			}
		}
	}

	return nil
}
