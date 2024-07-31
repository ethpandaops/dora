package db

import (
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertUnfinalizedEpoch(epoch *dbtypes.Epoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
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
				sync_participation = excluded.sync_participation`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO unfinalized_epochs (
				epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
				attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count, 
				proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
	}),
		epoch.Epoch, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget, epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount,
		epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount, epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount,
		epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation)
	if err != nil {
		return err
	}
	return nil
}

func GetUnfinalizedEpoch(epoch uint64) *dbtypes.Epoch {
	epochDuty := dbtypes.Epoch{}
	err := ReaderDb.Get(&epochDuty, `
	SELECT
		epoch, validator_count, validator_balance, eligible, voted_target, voted_head, voted_total, block_count, orphaned_count,
		attestation_count, deposit_count, exit_count, withdraw_count, withdraw_amount, attester_slashing_count,
		proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation
	FROM unfinalized_epochs
	WHERE epoch = $1
	`, epoch)
	if err != nil {
		return nil
	}
	return &epochDuty
}

func DeleteUnfinalizedEpochsBefore(epoch uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM unfinalized_epochs WHERE epoch < $1`, epoch)
	if err != nil {
		return err
	}
	return nil
}
