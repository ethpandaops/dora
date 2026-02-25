package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertBuilder inserts a single builder into the database
func InsertBuilder(builder *dbtypes.Builder, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO builders (
				pubkey, builder_index, version, execution_address,
				deposit_epoch, withdrawable_epoch, superseded
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (pubkey) DO UPDATE SET
				builder_index = excluded.builder_index,
				version = excluded.version,
				execution_address = excluded.execution_address,
				deposit_epoch = excluded.deposit_epoch,
				withdrawable_epoch = excluded.withdrawable_epoch,
				superseded = excluded.superseded`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO builders (
				pubkey, builder_index, version, execution_address,
				deposit_epoch, withdrawable_epoch, superseded
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
	}),
		builder.Pubkey,
		builder.BuilderIndex,
		builder.Version,
		builder.ExecutionAddress,
		builder.DepositEpoch,
		builder.WithdrawableEpoch,
		builder.Superseded)

	if err != nil {
		return fmt.Errorf("error inserting builder: %w", err)
	}
	return nil
}

// InsertBuilderBatch inserts multiple builders in a batch
func InsertBuilderBatch(builders []*dbtypes.Builder, tx *sqlx.Tx) error {
	if len(builders) == 0 {
		return nil
	}

	valueStrings := make([]string, len(builders))
	valueArgs := make([]any, 0, len(builders)*7)
	for i, b := range builders {
		valueStrings[i] = fmt.Sprintf("($%v, $%v, $%v, $%v, $%v, $%v, $%v)",
			i*7+1, i*7+2, i*7+3, i*7+4, i*7+5, i*7+6, i*7+7)
		valueArgs = append(valueArgs,
			b.Pubkey,
			b.BuilderIndex,
			b.Version,
			b.ExecutionAddress,
			b.DepositEpoch,
			b.WithdrawableEpoch,
			b.Superseded)
	}

	stmt := fmt.Sprintf(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO builders (
				pubkey, builder_index, version, execution_address,
				deposit_epoch, withdrawable_epoch, superseded
			) VALUES %s
			ON CONFLICT (pubkey) DO UPDATE SET
				builder_index = excluded.builder_index,
				version = excluded.version,
				execution_address = excluded.execution_address,
				deposit_epoch = excluded.deposit_epoch,
				withdrawable_epoch = excluded.withdrawable_epoch,
				superseded = excluded.superseded`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO builders (
				pubkey, builder_index, version, execution_address,
				deposit_epoch, withdrawable_epoch, superseded
			) VALUES %s`,
	}), strings.Join(valueStrings, ","))

	_, err := tx.Exec(stmt, valueArgs...)
	if err != nil {
		return fmt.Errorf("error inserting builder batch: %w", err)
	}

	return nil
}

// GetBuilderByPubkey returns a builder by pubkey (primary key)
func GetBuilderByPubkey(ctx context.Context, pubkey []byte) *dbtypes.Builder {
	builder := dbtypes.Builder{}
	err := ReaderDb.GetContext(ctx, &builder, `
		SELECT * FROM builders WHERE pubkey = $1
	`, pubkey)
	if err != nil {
		return nil
	}
	return &builder
}

// GetActiveBuilderByIndex returns the active (non-superseded) builder for a given index
func GetActiveBuilderByIndex(ctx context.Context, index uint64) *dbtypes.Builder {
	builder := dbtypes.Builder{}
	err := ReaderDb.GetContext(ctx, &builder, `
		SELECT * FROM builders WHERE builder_index = $1 AND superseded = false
	`, index)
	if err != nil {
		return nil
	}
	return &builder
}

// GetBuildersByIndex returns all builders (including superseded) for a given index
func GetBuildersByIndex(ctx context.Context, index uint64) []*dbtypes.Builder {
	builders := []*dbtypes.Builder{}
	err := ReaderDb.SelectContext(ctx, &builders, `
		SELECT * FROM builders WHERE builder_index = $1 ORDER BY superseded ASC
	`, index)
	if err != nil {
		logger.Errorf("Error while fetching builders by index: %v", err)
		return nil
	}
	return builders
}

// GetBuilderRange returns builders in a given index range (only active builders)
func GetBuilderRange(ctx context.Context, startIndex uint64, endIndex uint64) []*dbtypes.Builder {
	builders := []*dbtypes.Builder{}
	err := ReaderDb.SelectContext(ctx, &builders, `
		SELECT * FROM builders
		WHERE builder_index >= $1 AND builder_index <= $2 AND superseded = false
		ORDER BY builder_index ASC
	`, startIndex, endIndex)
	if err != nil {
		logger.Errorf("Error while fetching builder range: %v", err)
		return nil
	}
	return builders
}

// GetMaxBuilderIndex returns the highest builder index in the database
func GetMaxBuilderIndex(ctx context.Context) (uint64, error) {
	var maxIndex uint64
	err := ReaderDb.GetContext(ctx, &maxIndex, "SELECT COALESCE(MAX(builder_index), 0) FROM builders")
	if err != nil {
		return 0, fmt.Errorf("error getting max builder index: %w", err)
	}
	return maxIndex, nil
}

// GetBuilderCount returns the count of builders (optionally only active)
func GetBuilderCount(ctx context.Context, activeOnly bool) (uint64, error) {
	var count uint64
	var err error
	if activeOnly {
		err = ReaderDb.GetContext(ctx, &count, "SELECT COUNT(*) FROM builders WHERE superseded = false")
	} else {
		err = ReaderDb.GetContext(ctx, &count, "SELECT COUNT(*) FROM builders")
	}
	if err != nil {
		return 0, fmt.Errorf("error getting builder count: %w", err)
	}
	return count, nil
}

// SetBuilderSuperseded marks a builder as superseded
func SetBuilderSuperseded(pubkey []byte, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		UPDATE builders SET superseded = true WHERE pubkey = $1
	`, pubkey)
	if err != nil {
		return fmt.Errorf("error setting builder superseded: %w", err)
	}
	return nil
}

// SetBuildersSuperseded marks multiple builders as superseded in a batch
func SetBuildersSuperseded(pubkeys [][]byte, tx *sqlx.Tx) error {
	if len(pubkeys) == 0 {
		return nil
	}

	var sql strings.Builder
	sql.WriteString("UPDATE builders SET superseded = true WHERE pubkey IN (")

	args := make([]any, len(pubkeys))
	for i, pk := range pubkeys {
		if i > 0 {
			sql.WriteString(", ")
		}
		fmt.Fprintf(&sql, "$%d", i+1)
		args[i] = pk
	}
	sql.WriteString(")")

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return fmt.Errorf("error setting builders superseded: %w", err)
	}
	return nil
}

// StreamBuildersByPubkeys streams builders by pubkeys in batches
func StreamBuildersByPubkeys(ctx context.Context, pubkeys [][]byte, cb func(builder *dbtypes.Builder) bool) error {
	const batchSize = 1000

	for i := 0; i < len(pubkeys); i += batchSize {
		end := min(i+batchSize, len(pubkeys))
		batch := pubkeys[i:end]

		var sql strings.Builder
		fmt.Fprintf(&sql, `
		SELECT
			pubkey, builder_index, version, execution_address,
			deposit_epoch, withdrawable_epoch, superseded
		FROM builders
		WHERE pubkey in (`)

		args := make([]any, len(batch))
		for j, pk := range batch {
			if j > 0 {
				fmt.Fprintf(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", j+1)
			args[j] = pk
		}
		fmt.Fprintf(&sql, ")")

		// Create pubkey map for ordering
		pubkeyMap := make(map[string]int, len(batch))
		for pos, pk := range batch {
			pubkeyMap[string(pk)] = pos
		}

		// Fetch all builders for this batch
		builders := make([]*dbtypes.Builder, len(batch))
		rows, err := ReaderDb.QueryContext(ctx, sql.String(), args...)
		if err != nil {
			return fmt.Errorf("error querying builders: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			builder := &dbtypes.Builder{}
			err := rows.Scan(
				&builder.Pubkey,
				&builder.BuilderIndex,
				&builder.Version,
				&builder.ExecutionAddress,
				&builder.DepositEpoch,
				&builder.WithdrawableEpoch,
				&builder.Superseded,
			)
			if err != nil {
				return fmt.Errorf("error scanning builder: %w", err)
			}
			pos := pubkeyMap[string(builder.Pubkey)]
			builders[pos] = builder
		}

		if err = rows.Err(); err != nil {
			return fmt.Errorf("error iterating rows: %w", err)
		}

		// Stream in original order
		for _, b := range builders {
			if b != nil && !cb(b) {
				return nil
			}
		}
	}

	return nil
}

// GetBuildersByExecutionAddress returns builders with a specific execution address
func GetBuildersByExecutionAddress(ctx context.Context, address []byte) []*dbtypes.Builder {
	builders := []*dbtypes.Builder{}
	err := ReaderDb.SelectContext(ctx, &builders, `
		SELECT * FROM builders WHERE execution_address = $1 ORDER BY builder_index ASC
	`, address)
	if err != nil {
		logger.Errorf("Error while fetching builders by execution address: %v", err)
		return nil
	}
	return builders
}

// GetBuilderIndexesByFilter returns builder indexes matching a filter
func GetBuilderIndexesByFilter(ctx context.Context, filter dbtypes.BuilderFilter, currentEpoch uint64) ([]uint64, error) {
	var sql strings.Builder
	args := []interface{}{}
	fmt.Fprint(&sql, `
	SELECT
		builder_index
	FROM builders
	`)

	args = buildBuilderFilterSql(filter, currentEpoch, &sql, args)

	switch filter.OrderBy {
	case dbtypes.BuilderOrderIndexAsc:
		fmt.Fprint(&sql, " ORDER BY builder_index ASC")
	case dbtypes.BuilderOrderIndexDesc:
		fmt.Fprint(&sql, " ORDER BY builder_index DESC")
	case dbtypes.BuilderOrderPubKeyAsc:
		fmt.Fprint(&sql, " ORDER BY pubkey ASC")
	case dbtypes.BuilderOrderPubKeyDesc:
		fmt.Fprint(&sql, " ORDER BY pubkey DESC")
	case dbtypes.BuilderOrderDepositEpochAsc:
		fmt.Fprint(&sql, " ORDER BY deposit_epoch ASC")
	case dbtypes.BuilderOrderDepositEpochDesc:
		fmt.Fprint(&sql, " ORDER BY deposit_epoch DESC")
	case dbtypes.BuilderOrderWithdrawableEpochAsc:
		fmt.Fprint(&sql, " ORDER BY withdrawable_epoch ASC")
	case dbtypes.BuilderOrderWithdrawableEpochDesc:
		fmt.Fprint(&sql, " ORDER BY withdrawable_epoch DESC")
	}

	builderIds := []uint64{}
	err := ReaderDb.SelectContext(ctx, &builderIds, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching builders by filter: %v", err)
		return nil, err
	}

	return builderIds, nil
}

func buildBuilderFilterSql(filter dbtypes.BuilderFilter, currentEpoch uint64, sql *strings.Builder, args []interface{}) []interface{} {
	filterOp := "WHERE"

	if filter.MinIndex != nil {
		fmt.Fprintf(sql, " %v builder_index >= $%v", filterOp, len(args)+1)
		args = append(args, *filter.MinIndex)
		filterOp = "AND"
	}
	if filter.MaxIndex != nil {
		fmt.Fprintf(sql, " %v builder_index <= $%v", filterOp, len(args)+1)
		args = append(args, *filter.MaxIndex)
		filterOp = "AND"
	}
	if len(filter.PubKey) > 0 {
		fmt.Fprintf(sql, " %v pubkey LIKE $%v", filterOp, len(args)+1)
		args = append(args, append(filter.PubKey, '%'))
		filterOp = "AND"
	}
	if len(filter.ExecutionAddress) > 0 {
		fmt.Fprintf(sql, " %v execution_address = $%v", filterOp, len(args)+1)
		args = append(args, filter.ExecutionAddress)
		filterOp = "AND"
	}
	if len(filter.Status) > 0 {
		statusConditions := make([]string, 0, len(filter.Status))
		for _, status := range filter.Status {
			switch status {
			case dbtypes.BuilderStatusActiveFilter:
				statusConditions = append(statusConditions, fmt.Sprintf("(superseded = false AND withdrawable_epoch > $%v)", len(args)+1))
				args = append(args, ConvertUint64ToInt64(currentEpoch))
			case dbtypes.BuilderStatusExitedFilter:
				statusConditions = append(statusConditions, fmt.Sprintf("(superseded = false AND withdrawable_epoch <= $%v)", len(args)+1))
				args = append(args, ConvertUint64ToInt64(currentEpoch))
			case dbtypes.BuilderStatusSupersededFilter:
				statusConditions = append(statusConditions, "superseded = true")
			}
		}
		if len(statusConditions) > 0 {
			fmt.Fprintf(sql, " %v (%v)", filterOp, strings.Join(statusConditions, " OR "))
		}
	}

	return args
}

// StreamBuildersByIndexes streams builders by indexes
func StreamBuildersByIndexes(ctx context.Context, indexes []uint64, cb func(builder *dbtypes.Builder) bool) {
	const batchSize = 1000

	for i := 0; i < len(indexes); i += batchSize {
		end := min(i+batchSize, len(indexes))
		batch := indexes[i:end]

		var sql strings.Builder
		fmt.Fprint(&sql, `
		SELECT
			pubkey, builder_index, version, execution_address,
			deposit_epoch, withdrawable_epoch, superseded
		FROM builders
		WHERE builder_index IN (`)

		args := make([]any, len(batch))
		for j, idx := range batch {
			if j > 0 {
				fmt.Fprint(&sql, ", ")
			}
			fmt.Fprintf(&sql, "$%v", j+1)
			args[j] = idx
		}
		fmt.Fprint(&sql, ")")

		// Create index map for ordering
		indexMap := make(map[uint64]int, len(batch))
		for pos, idx := range batch {
			indexMap[idx] = pos
		}

		// Fetch all builders for this batch
		builders := make([]*dbtypes.Builder, len(batch))
		rows, err := ReaderDb.QueryContext(ctx, sql.String(), args...)
		if err != nil {
			logger.Errorf("Error querying builders: %v", err)
			return
		}

		for rows.Next() {
			builder := &dbtypes.Builder{}
			err := rows.Scan(
				&builder.Pubkey,
				&builder.BuilderIndex,
				&builder.Version,
				&builder.ExecutionAddress,
				&builder.DepositEpoch,
				&builder.WithdrawableEpoch,
				&builder.Superseded,
			)
			if err != nil {
				logger.Errorf("Error scanning builder: %v", err)
				rows.Close()
				return
			}
			pos := indexMap[builder.BuilderIndex]
			builders[pos] = builder
		}
		rows.Close()

		// Stream in original order
		for _, b := range builders {
			if b != nil && !cb(b) {
				return
			}
		}
	}
}
