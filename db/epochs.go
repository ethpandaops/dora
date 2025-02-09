package db

import (
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertEpoch(epoch *dbtypes.Epoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
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
				payload_count = excluded.payload_count`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
	}),
		epoch.Epoch, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount, epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount,
		epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation, epoch.PayloadCount)
	if err != nil {
		return err
	}
	return nil
}

func IsEpochSynchronized(epoch uint64) bool {
	var count uint64
	err := ReaderDb.Get(&count, `SELECT COUNT(*) FROM epochs WHERE epoch = $1`, epoch)
	if err != nil {
		return false
	}
	return count > 0
}

func GetEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	epochs := []*dbtypes.Epoch{}
	err := ReaderDb.Select(&epochs, `
	SELECT
		epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count,
		proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, payload_count
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
