package db

import (
	"context"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertEpoch(ctx context.Context, tx *sqlx.Tx, epoch *dbtypes.Epoch) error {
	_, err := tx.ExecContext(ctx, EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, blob_count,
				eth_gas_used, eth_gas_limit, payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
			ON CONFLICT (epoch) DO UPDATE SET
				validator_count = excluded.validator_count,
				validator_balance = excluded.validator_balance,
				eligible = excluded.eligible,
				voted_target = excluded.voted_target,
				voted_head = excluded.voted_head, 
				voted_total = excluded.voted_total, 
				block_count = excluded.block_count,
				orphaned_count = excluded.orphaned_count,
				attestation_count = excluded.attestation_count, 
				deposit_count = excluded.deposit_count, 
				exit_count = excluded.exit_count, 
				withdraw_count = excluded.withdraw_count, 
				withdraw_amount = excluded.withdraw_amount, 
				attester_slashing_count = excluded.attester_slashing_count, 
				proposer_slashing_count = excluded.proposer_slashing_count, 
				bls_change_count = excluded.bls_change_count, 
				eth_transaction_count = excluded.eth_transaction_count, 
				sync_participation = excluded.sync_participation,
				blob_count = excluded.blob_count,
				eth_gas_used = excluded.eth_gas_used,
				eth_gas_limit = excluded.eth_gas_limit,
				payload_count = excluded.payload_count`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, blob_count,
				eth_gas_used, eth_gas_limit, payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
	}),
		epoch.Epoch, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount, epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount,
		epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation, epoch.BlobCount, epoch.EthGasUsed, epoch.EthGasLimit, epoch.PayloadCount)
	if err != nil {
		return err
	}
	return nil
}

func IsEpochSynchronized(ctx context.Context, epoch uint64) bool {
	var count uint64
	err := ReaderDb.GetContext(ctx, &count, `SELECT COUNT(*) FROM epochs WHERE epoch = $1`, epoch)
	if err != nil {
		return false
	}
	return count > 0
}

func GetEpochs(ctx context.Context, firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	epochs := []*dbtypes.Epoch{}
	err := ReaderDb.SelectContext(ctx, &epochs, `
	SELECT
		epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count,
		proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, blob_count,
		eth_gas_used, eth_gas_limit, payload_count
	FROM epochs
	WHERE epoch <= $1
	ORDER BY epoch DESC
	LIMIT $2
	`, firstEpoch, limit)
	if err != nil {
		logger.Errorf("Error while fetching epochs: %v", err)
		return nil
	}
	return epochs
}

// EpochParticipation represents participation data for a finalized canonical epoch
type EpochParticipation struct {
	Epoch       uint64 `db:"epoch"`
	BlockCount  uint64 `db:"block_count"`
	Eligible    uint64 `db:"eligible"`
	VotedTarget uint64 `db:"voted_target"`
	VotedHead   uint64 `db:"voted_head"`
	VotedTotal  uint64 `db:"voted_total"`
}

// GetFinalizedEpochParticipation gets participation data for finalized canonical epochs in the given range
func GetFinalizedEpochParticipation(ctx context.Context, startEpoch, endEpoch uint64) ([]*EpochParticipation, error) {
	var results []*EpochParticipation

	err := ReaderDb.SelectContext(ctx, &results, `
		SELECT 
			epoch,
			block_count,
			eligible,
			voted_target,
			voted_head,
			voted_total
		FROM epochs
		WHERE epoch >= $1 AND epoch <= $2
		ORDER BY epoch
	`, startEpoch, endEpoch)

	if err != nil {
		return nil, err
	}

	return results, nil
}
