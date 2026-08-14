package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// GetEnsNamesByAddresses retrieves persisted ENS lookups (positive and negative, all
// networks) for multiple addresses in a single query. Returns a map from address
// (hex string) to the stored per-network entries for efficient lookup.
func GetEnsNamesByAddresses(ctx context.Context, addresses [][]byte) (map[string][]*dbtypes.EnsName, error) {
	if len(addresses) == 0 {
		return make(map[string][]*dbtypes.EnsName), nil
	}

	var sql strings.Builder
	args := make([]any, len(addresses))

	fmt.Fprint(&sql, "SELECT address, network, name, resolved_time FROM ens_names WHERE address IN (")
	for i, addr := range addresses {
		args[i] = addr
	}
	appendDollarPlaceholders(&sql, 1, len(addresses), ", ")
	fmt.Fprint(&sql, ")")

	names := []*dbtypes.EnsName{}
	err := ReaderDb.SelectContext(ctx, &names, sql.String(), args...)
	if err != nil {
		logger.Errorf("Error while fetching ens names by addresses: %v", err)
		return nil, err
	}

	result := make(map[string][]*dbtypes.EnsName, len(names))
	for _, name := range names {
		key := byteSliceMapKey(name.Address)
		result[key] = append(result[key], name)
	}
	return result, nil
}

// GetEnsNamesByPrefix retrieves persisted positive ENS lookups whose name starts with
// the given prefix (case-sensitive; stored names are lowercase), across all networks.
func GetEnsNamesByPrefix(ctx context.Context, prefix string, limit int) ([]*dbtypes.EnsName, error) {
	if prefix == "" || limit <= 0 {
		return nil, nil
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)

	names := []*dbtypes.EnsName{}
	err := ReaderDb.SelectContext(ctx, &names, `
		SELECT address, network, name, resolved_time
		FROM ens_names
		WHERE name LIKE $1 ESCAPE '\' AND name != ''
		ORDER BY name ASC
		LIMIT $2`, escaped+"%", limit)
	if err != nil {
		logger.Errorf("Error while fetching ens names by prefix: %v", err)
		return nil, err
	}
	return names, nil
}

// InsertEnsNames upserts a batch of ENS lookups (positive and negative), refreshing
// the name and resolved_time for (address, network) pairs that already exist.
func InsertEnsNames(ctx context.Context, dbTx *sqlx.Tx, names []*dbtypes.EnsName) error {
	if len(names) == 0 {
		return nil
	}

	var sql strings.Builder
	args := make([]any, 0, len(names)*4)

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql:  "INSERT INTO ens_names (address, network, name, resolved_time) VALUES ",
		dbtypes.DBEngineSqlite: "INSERT OR REPLACE INTO ens_names (address, network, name, resolved_time) VALUES ",
	}))

	for i, name := range names {
		if i > 0 {
			fmt.Fprint(&sql, ", ")
		}
		argIdx := len(args) + 1
		fmt.Fprintf(&sql, "($%d, $%d, $%d, $%d)", argIdx, argIdx+1, argIdx+2, argIdx+3)
		args = append(args, name.Address, name.Network, name.Name, name.ResolvedTime)
	}

	fmt.Fprint(&sql, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: " ON CONFLICT (address, network) DO UPDATE SET name = excluded.name, resolved_time = excluded.resolved_time",
	}))

	_, err := dbTx.ExecContext(ctx, sql.String(), args...)
	if err != nil {
		return fmt.Errorf("error inserting ens names: %w", err)
	}
	return nil
}
