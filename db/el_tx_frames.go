package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// elTxFrameColumns is the column list for el_tx_frames queries.
const elTxFrameColumns = "tx_uid, frame_index, mode, flags, status, rolled_back, to_id, amount, amount_raw, method_id, data_len, exec_gas_limit, state_gas_limit, exec_gas_used, state_gas_used, log_count, trace_count"

// InsertElTxFrames writes the frames of one or more EIP-8141 frame transactions.
func InsertElTxFrames(ctx context.Context, dbTx *sqlx.Tx, frames []*dbtypes.ElTxFrame) error {
	if len(frames) == 0 {
		return nil
	}

	var sql strings.Builder

	fmt.Fprint(&sql,
		EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql:  "INSERT INTO el_tx_frames ",
			dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO el_tx_frames ",
		}),
		"("+elTxFrameColumns+")",
		" VALUES ",
	)

	fieldCount := 17
	argIdx := 0
	args := make([]any, len(frames)*fieldCount)

	for i, frame := range frames {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}

		fmt.Fprint(&sql, "(")

		for f := 0; f < fieldCount; f++ {
			if f > 0 {
				fmt.Fprint(&sql, ", ")
			}

			fmt.Fprintf(&sql, "$%v", argIdx+f+1)
		}

		fmt.Fprint(&sql, ")")

		args[argIdx+0] = frame.TxUid
		args[argIdx+1] = frame.FrameIndex
		args[argIdx+2] = frame.Mode
		args[argIdx+3] = frame.Flags
		args[argIdx+4] = frame.Status
		args[argIdx+5] = frame.RolledBack
		args[argIdx+6] = frame.ToID
		args[argIdx+7] = frame.Amount
		args[argIdx+8] = frame.AmountRaw
		args[argIdx+9] = frame.MethodID
		args[argIdx+10] = frame.DataLen
		args[argIdx+11] = frame.ExecGasLimit
		args[argIdx+12] = frame.StateGasLimit
		args[argIdx+13] = frame.ExecGasUsed
		args[argIdx+14] = frame.StateGasUsed
		args[argIdx+15] = frame.LogCount
		args[argIdx+16] = frame.TraceCount
		argIdx += fieldCount
	}

	// Re-indexing a block rewrites its frames rather than duplicating them.
	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (tx_uid, frame_index) DO UPDATE SET" +
			" mode = excluded.mode, flags = excluded.flags, status = excluded.status," +
			" rolled_back = excluded.rolled_back, to_id = excluded.to_id, amount = excluded.amount," +
			" amount_raw = excluded.amount_raw, method_id = excluded.method_id," +
			" data_len = excluded.data_len, exec_gas_limit = excluded.exec_gas_limit," +
			" state_gas_limit = excluded.state_gas_limit, exec_gas_used = excluded.exec_gas_used," +
			" state_gas_used = excluded.state_gas_used, log_count = excluded.log_count," +
			" trace_count = excluded.trace_count",
		dbtypes.DBEngineSqlite: "",
	}))

	if _, err := dbTx.ExecContext(ctx, sql.String(), args...); err != nil {
		return err
	}

	return nil
}

// GetElTxFramesByTxUid returns one transaction's frames in frame order.
func GetElTxFramesByTxUid(ctx context.Context, txUid uint64) ([]*dbtypes.ElTxFrame, error) {
	frames := []*dbtypes.ElTxFrame{}

	err := ReaderDb.SelectContext(ctx, &frames,
		"SELECT "+elTxFrameColumns+" FROM el_tx_frames WHERE tx_uid = $1 ORDER BY frame_index ASC",
		txUid,
	)
	if err != nil {
		logger.Errorf("Error while fetching el tx frames: %v", err)

		return nil, err
	}

	return frames, nil
}
