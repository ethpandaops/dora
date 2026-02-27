package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertUnfinalizedBlock(ctx context.Context, tx *sqlx.Tx, block *dbtypes.UnfinalizedBlock) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_blocks (
				root, slot, header_ver, header_ssz, block_ver, block_ssz, status, fork_id, recv_delay, min_exec_time, max_exec_time, exec_times, 
				block_uid
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (root) DO NOTHING`,
		dbtypes.DBEngineSqlite: `
			INSERT OR IGNORE INTO unfinalized_blocks (
				root, slot, header_ver, header_ssz, block_ver, block_ssz, status, fork_id, recv_delay, min_exec_time, max_exec_time, exec_times,
				block_uid
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
	}),
		block.Root, block.Slot, block.HeaderVer, block.HeaderSSZ, block.BlockVer, block.BlockSSZ, block.Status, block.ForkId, block.RecvDelay, block.MinExecTime, block.MaxExecTime,
		block.ExecTimes, block.BlockUid,
	)
	if err != nil {
		return err
	}
	return nil
}

func UpdateUnfinalizedBlockStatus(ctx context.Context, tx *sqlx.Tx, roots [][]byte, blockStatus dbtypes.UnfinalizedBlockStatus) error {
	if len(roots) == 0 {
		return nil
	}

	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `UPDATE unfinalized_blocks SET status = $1 WHERE root IN (`)
	args = append(args, blockStatus)

	for i, root := range roots {
		if i > 0 {
			fmt.Fprint(&sql, ",")
		}

		args = append(args, root)
		fmt.Fprintf(&sql, "$%v", len(args))
	}

	fmt.Fprint(&sql, ")")

	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func UpdateUnfinalizedBlockForkId(ctx context.Context, tx *sqlx.Tx, roots [][]byte, forkId uint64) error {
	if len(roots) == 0 {
		return nil
	}

	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `UPDATE unfinalized_blocks SET fork_id = $1 WHERE root IN (`)
	args = append(args, forkId)

	for i, root := range roots {
		if i > 0 {
			fmt.Fprint(&sql, ",")
		}

		args = append(args, root)
		fmt.Fprintf(&sql, "$%v", len(args))
	}

	fmt.Fprint(&sql, ")")

	_, err := tx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return err
	}
	return nil
}

func UpdateUnfinalizedBlockExecutionTimes(ctx context.Context, tx *sqlx.Tx, root []byte, minExecTime uint32, maxExecTime uint32, execTimes []byte) error {
	_, err := tx.ExecContext(ctx, `UPDATE unfinalized_blocks SET min_exec_time = $1, max_exec_time = $2, exec_times = $3 WHERE root = $4`, minExecTime, maxExecTime, execTimes, root)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedBlocks(ctx context.Context, filter *dbtypes.UnfinalizedBlockFilter) []*dbtypes.UnfinalizedBlock {
	blockRefs := []*dbtypes.UnfinalizedBlock{}

	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `SELECT root, slot, status, fork_id, header_ver, header_ssz, recv_delay, min_exec_time, max_exec_time, exec_times, block_uid`)

	if filter == nil || filter.WithBody {
		fmt.Fprint(&sql, `, block_ver, block_ssz`)
	}
	fmt.Fprint(&sql, `
	FROM unfinalized_blocks
	`)

	if filter != nil {
		filterOp := "WHERE"

		if filter.MinSlot > 0 {
			args = append(args, filter.MinSlot)
			fmt.Fprintf(&sql, " %v slot >= $%v", filterOp, len(args))
			filterOp = "AND"
		}

		if filter.MaxSlot > 0 {
			args = append(args, filter.MaxSlot)
			fmt.Fprintf(&sql, " %v slot <= $%v", filterOp, len(args))
			filterOp = "AND"
		}
	}

	err := ReaderDb.SelectContext(ctx, &blockRefs, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized blocks: %v", err)
		return nil
	}
	return blockRefs
}

func StreamUnfinalizedBlocks(ctx context.Context, slot uint64, cb func(block *dbtypes.UnfinalizedBlock)) error {
	var sql strings.Builder
	args := []any{slot}

	fmt.Fprint(&sql, `SELECT root, slot, header_ver, header_ssz, block_ver, block_ssz, status, fork_id, recv_delay, min_exec_time, max_exec_time, exec_times, block_uid FROM unfinalized_blocks WHERE slot >= $1`)

	rows, err := ReaderDb.QueryContext(ctx, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized blocks: %v", err)
		return nil
	}

	for rows.Next() {
		block := dbtypes.UnfinalizedBlock{}
		err := rows.Scan(
			&block.Root, &block.Slot, &block.HeaderVer, &block.HeaderSSZ, &block.BlockVer, &block.BlockSSZ, &block.Status, &block.ForkId, &block.RecvDelay,
			&block.MinExecTime, &block.MaxExecTime, &block.ExecTimes, &block.BlockUid,
		)
		if err != nil {
			logger.Errorf("Error while scanning unfinalized block: %v", err)
			return err
		}
		cb(&block)
	}

	return nil
}

func GetUnfinalizedBlock(ctx context.Context, root []byte) *dbtypes.UnfinalizedBlock {
	block := dbtypes.UnfinalizedBlock{}
	err := ReaderDb.GetContext(ctx, &block, `
	SELECT root, slot, header_ver, header_ssz, block_ver, block_ssz, status, fork_id, recv_delay, min_exec_time, max_exec_time, exec_times, block_uid
	FROM unfinalized_blocks
	WHERE root = $1
	`, root)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized block 0x%x: %v", root, err)
		return nil
	}
	return &block
}

func DeleteUnfinalizedBlocksBefore(ctx context.Context, tx *sqlx.Tx, slot uint64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM unfinalized_blocks WHERE slot < $1`, slot)
	if err != nil {
		return err
	}
	return nil
}
