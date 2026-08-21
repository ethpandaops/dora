package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExecutionRequestTxTablesAreComplete guards that every execution-layer request table that
// carries a fork_id is re-tagged. A table added later without being listed here would silently
// keep stale fork ids, which reads as "canonical transaction shown as orphaned" long after the
// fork it was indexed under is gone.
func TestExecutionRequestTxTablesAreComplete(t *testing.T) {
	require.ElementsMatch(t, []string{
		"deposit_txs",
		"builder_deposit_request_txs",
		"builder_exit_request_txs",
		"withdrawal_request_txs",
		"consolidation_request_txs",
	}, executionRequestTxTables)

	for _, table := range executionRequestTxTables {
		require.True(t, strings.HasSuffix(table, "_txs"),
			"%v is not a request-transaction table; consensus rows follow their block instead", table)
	}
}

func TestUpdateForkIdBatchStaysUnderBindLimits(t *testing.T) {
	// one bind for the fork id plus one per block hash, against the 999-variable limit of older
	// SQLite builds
	require.LessOrEqual(t, updateForkIdBatchSize+1, 999)
}
