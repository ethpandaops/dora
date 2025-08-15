package db

import (
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertFork(fork *dbtypes.Fork, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO forks (
				fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (fork_id) DO UPDATE
			SET 
				base_slot = excluded.base_slot,
				base_root = excluded.base_root,
				leaf_slot = excluded.leaf_slot,
				leaf_root = excluded.leaf_root,
				parent_fork = excluded.parent_fork;`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO forks (
				fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
			) VALUES ($1, $2, $3, $4, $5, $6)`,
	}),
		fork.ForkId, fork.BaseSlot, fork.BaseRoot, fork.LeafSlot, fork.LeafRoot, fork.ParentFork)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedForks(finalizedSlot uint64) []*dbtypes.Fork {
	forks := []*dbtypes.Fork{}

	err := ReaderDb.Select(&forks, `SELECT fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
		FROM forks
		WHERE base_slot >= $1
		ORDER BY base_slot ASC
	`, finalizedSlot)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized forks: %v", err)
		return nil
	}

	return forks
}

func DeleteFinalizedForks(finalizedRoots [][]byte, tx *sqlx.Tx) error {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `DELETE FROM forks WHERE leaf_root IN (`)

	for i, root := range finalizedRoots {
		if i > 0 {
			fmt.Fprint(&sql, ",")
		}

		args = append(args, root)
		fmt.Fprintf(&sql, "$%v", len(args))
	}

	fmt.Fprint(&sql, ")")

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}

	sql = strings.Builder{}
	args = []any{}

	fmt.Fprint(&sql, `UPDATE forks SET parent_fork = 0 WHERE base_root IN (`)

	for i, root := range finalizedRoots {
		if i > 0 {
			fmt.Fprint(&sql, ",")
		}

		args = append(args, root)
		fmt.Fprintf(&sql, "$%v", len(args))
	}

	fmt.Fprint(&sql, ")")

	_, err = tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}

	return nil
}

func UpdateFinalizedForkParents(finalizedRoots [][]byte, tx *sqlx.Tx) error {
	var sql strings.Builder
	args := []any{}

	fmt.Fprint(&sql, `
		UPDATE forks 
		SET parent_fork = 0 
		WHERE fork_id IN (
			SELECT fork_id FROM forks WHERE leaf_root IN (
	`)

	for i, root := range finalizedRoots {
		if i > 0 {
			fmt.Fprint(&sql, ",")
		}

		args = append(args, root)
		fmt.Fprintf(&sql, "$%v", len(args))
	}

	fmt.Fprint(&sql, "))")

	_, err := tx.Exec(sql.String(), args...)
	if err != nil {
		return err
	}

	return nil
}

func UpdateForkParent(parentRoot []byte, parentForkId uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		UPDATE forks 
		SET parent_fork = $1 
		WHERE base_root = $2
	`, parentForkId, parentRoot)
	if err != nil {
		return err
	}

	return nil
}

func GetForkById(forkId uint64) *dbtypes.Fork {
	var fork dbtypes.Fork

	err := ReaderDb.Get(&fork, `SELECT fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
		FROM forks
		WHERE fork_id = $1
	`, forkId)
	if err != nil {
		return nil
	}

	return &fork
}

func GetForkVisualizationData(startSlot uint64, pageSize uint64) ([]*dbtypes.Fork, error) {
	forks := []*dbtypes.Fork{}

	// Get forks that overlap with our time window or are parents of forks in our window
	err := ReaderDb.Select(&forks, `
		WITH RECURSIVE fork_tree AS (
			-- Base case: forks that intersect with our time window
			SELECT fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
			FROM forks 
			WHERE (base_slot >= $1 AND base_slot < $2) 
			   OR (base_slot < $1 AND leaf_slot >= $1)
			
			UNION
			
			-- Recursive case: parent forks of forks already selected
			SELECT f.fork_id, f.base_slot, f.base_root, f.leaf_slot, f.leaf_root, f.parent_fork
			FROM forks f
			INNER JOIN fork_tree ft ON f.fork_id = ft.parent_fork
		)
		SELECT DISTINCT fork_id, base_slot, base_root, leaf_slot, leaf_root, parent_fork
		FROM fork_tree
		ORDER BY base_slot ASC, fork_id ASC
	`, startSlot, startSlot+pageSize)
	if err != nil {
		return nil, err
	}

	return forks, nil
}

// GetForkBlockCounts returns the number of blocks for each fork ID
func GetForkBlockCounts(startSlot uint64, endSlot uint64) (map[uint64]uint64, error) {
	args := []any{startSlot, endSlot}

	query := `
		SELECT fork_id, COUNT(*) as block_count
		FROM slots
		WHERE status != 0
		  AND slot >= $1 AND slot < $2
		GROUP BY fork_id
	`

	rows, err := ReaderDb.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	blockCounts := make(map[uint64]uint64)
	for rows.Next() {
		var forkId, count uint64
		if err := rows.Scan(&forkId, &count); err != nil {
			return nil, err
		}
		blockCounts[forkId] = count
	}

	return blockCounts, nil
}

func GetSyncCommitteeParticipation(slot uint64) (float64, error) {
	var participation float64

	// Handle potential overflow by using int64
	startSlot := int64(slot) - 1800
	endSlot := int64(slot) + 1800

	// Ensure we don't go below 0
	if startSlot < 0 {
		startSlot = 0
	}

	err := ReaderDb.Get(&participation, `
		SELECT 
			COALESCE(AVG(CASE WHEN sync_participation IS NOT NULL 
				THEN sync_participation ELSE 0 END), 0) as avg_participation
		FROM slots 
		WHERE slot >= $1 AND slot < $2
			AND sync_participation IS NOT NULL
	`, startSlot, endSlot)
	if err != nil {
		return 0, err
	}

	return participation, nil
}

// ForkParticipationByEpoch represents participation data for a fork in a specific epoch
type ForkParticipationByEpoch struct {
	ForkId        uint64  `db:"fork_id"`
	Epoch         uint64  `db:"epoch"`
	Participation float64 `db:"avg_participation"`
	SlotCount     uint64  `db:"slot_count"`
}

// GetForkParticipationByEpoch gets average participation per fork per epoch for the given epoch range
// This is optimized to fetch all data in one query to avoid expensive repeated queries
func GetForkParticipationByEpoch(startEpoch, endEpoch uint64, forkIds []uint64) ([]*ForkParticipationByEpoch, error) {
	if len(forkIds) == 0 {
		return []*ForkParticipationByEpoch{}, nil
	}

	// Build IN clause for fork IDs
	var forkPlaceholders strings.Builder
	args := make([]interface{}, 0, len(forkIds)+2)

	for i, forkId := range forkIds {
		if i > 0 {
			forkPlaceholders.WriteString(",")
		}
		args = append(args, forkId)
		fmt.Fprintf(&forkPlaceholders, "$%d", len(args))
	}

	// Add epoch range parameters
	args = append(args, startEpoch, endEpoch)
	startEpochParam := len(args) - 1
	endEpochParam := len(args)

	query := fmt.Sprintf(`
		SELECT 
			s.fork_id,
			(s.slot / 32) AS epoch,
			AVG(COALESCE(s.sync_participation, 0)) AS avg_participation,
			COUNT(*) AS slot_count
		FROM slots s
		WHERE s.fork_id IN (%s)
			AND (s.slot / 32) >= $%d AND (s.slot / 32) <= $%d
			AND s.sync_participation IS NOT NULL
		GROUP BY s.fork_id, (s.slot / 32)
		ORDER BY s.fork_id, (s.slot / 32)
	`, forkPlaceholders.String(), startEpochParam, endEpochParam)

	var results []*ForkParticipationByEpoch
	err := ReaderDb.Select(&results, query, args...)
	if err != nil {
		return nil, fmt.Errorf("error fetching fork participation by epoch: %w", err)
	}

	return results, nil
}
