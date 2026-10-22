package rpc

import (
	"context"
	"encoding/json"
)

// PbtMigrationProgress is the response of debug_migrationProgress, reported by execution
// clients that migrate their state to the binary trie (EIP-8347 partitioned binary trie).
//
// Clients differ in field casing (geth and besu use "Phase"/"Binary", nethermind uses
// "phase"/"binary"); encoding/json matches field names case-insensitively, so one struct
// decodes all of them. Erigon reports its dual-commitment mode and whether the shadow
// domain stopped instead of a cursor.
type PbtMigrationProgress struct {
	Phase         string            `json:"phase"`
	Binary        *PbtMigrationTree `json:"binary"`
	Merkle        *PbtMigrationTree `json:"merkle"`
	Mode          string            `json:"mode"`
	ShadowStopped *bool             `json:"shadowStopped"`
	Raw           json.RawMessage   `json:"-"`
}

// PbtMigrationTree is the progress of one trie in a dual-trie migration.
type PbtMigrationTree struct {
	Phase      string        `json:"phase"`
	Cursor     LenientUint64 `json:"cursor"`
	CursorHash string        `json:"cursorHash"`
	ShadowRoot string        `json:"shadowRoot"`
	Error      string        `json:"error"`
}

func (ec *ExecutionClient) GetPbtMigrationProgress(ctx context.Context) (*PbtMigrationProgress, error) {
	var raw json.RawMessage
	if err := ec.rpcClient.CallContext(ctx, &raw, "debug_migrationProgress"); err != nil {
		return nil, err
	}

	result := &PbtMigrationProgress{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, result); err != nil {
			return nil, err
		}
	}
	result.Raw = raw

	return result, nil
}
