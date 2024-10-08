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
