package db

import (
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

func InsertUnfinalizedEpoch(epoch *dbtypes.UnfinalizedEpoch, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO unfinalized_epochs (
				epoch, dependent_root, epoch_head_root, epoch_head_fork_id, validator_count, validator_balance, eligible, voted_target, 
				voted_head, voted_total, block_count, orphaned_count, attestation_count, deposit_count, exit_count, withdraw_count, 
				withdraw_amount, attester_slashing_count, proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation,
				payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
			ON CONFLICT (epoch, dependent_root, epoch_head_root) DO UPDATE SET
				epoch_head_fork_id = excluded.epoch_head_fork_id,
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
			INSERT OR REPLACE INTO unfinalized_epochs (
				epoch, dependent_root, epoch_head_root, epoch_head_fork_id, validator_count, validator_balance, eligible, voted_target, 
				voted_head, voted_total, block_count, orphaned_count, attestation_count, deposit_count, exit_count, withdraw_count, 
				withdraw_amount, attester_slashing_count, proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation,
				payload_count
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
	}),
		epoch.Epoch, epoch.DependentRoot, epoch.EpochHeadRoot, epoch.EpochHeadForkId, epoch.ValidatorCount, epoch.ValidatorBalance, epoch.Eligible, epoch.VotedTarget,
		epoch.VotedHead, epoch.VotedTotal, epoch.BlockCount, epoch.OrphanedCount, epoch.AttestationCount, epoch.DepositCount, epoch.ExitCount, epoch.WithdrawCount,
		epoch.WithdrawAmount, epoch.AttesterSlashingCount, epoch.ProposerSlashingCount, epoch.BLSChangeCount, epoch.EthTransactionCount, epoch.SyncParticipation,
		epoch.PayloadCount,
	)
	if err != nil {
		return err
	}
	return nil
}

func StreamUnfinalizedEpochs(epoch uint64, cb func(duty *dbtypes.UnfinalizedEpoch)) error {
	rows, err := ReaderDb.Query(`
	SELECT
		epoch, dependent_root, epoch_head_root, epoch_head_fork_id, validator_count, validator_balance, eligible, voted_target,
		voted_head, voted_total, block_count, orphaned_count, attestation_count, deposit_count, exit_count, withdraw_count,
		withdraw_amount, attester_slashing_count, proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation,
		payload_count
	FROM unfinalized_epochs
	WHERE epoch >= $1`, epoch)
	if err != nil {
		logger.Errorf("Error while fetching unfinalized epochs: %v", err)
		return nil
	}

	for rows.Next() {
		e := dbtypes.UnfinalizedEpoch{}
		err := rows.Scan(
			&e.Epoch, &e.DependentRoot, &e.EpochHeadRoot, &e.EpochHeadForkId, &e.ValidatorCount, &e.ValidatorBalance, &e.Eligible, &e.VotedTarget,
			&e.VotedHead, &e.VotedTotal, &e.BlockCount, &e.OrphanedCount, &e.AttestationCount, &e.DepositCount, &e.ExitCount, &e.WithdrawCount,
			&e.WithdrawAmount, &e.AttesterSlashingCount, &e.ProposerSlashingCount, &e.BLSChangeCount, &e.EthTransactionCount, &e.SyncParticipation,
			&e.PayloadCount,
		)
		if err != nil {
			logger.Errorf("Error while scanning unfinalized epoch: %v", err)
			return err
		}
		cb(&e)
	}

	return nil
}

func GetUnfinalizedEpoch(epoch uint64, headRoot []byte) *dbtypes.UnfinalizedEpoch {
	unfinalizedEpoch := dbtypes.UnfinalizedEpoch{}
	err := ReaderDb.Get(&unfinalizedEpoch, `
	SELECT
		epoch, dependent_root, epoch_head_root, epoch_head_fork_id, validator_count, validator_balance, eligible, voted_target,
		voted_head, voted_total, block_count, orphaned_count, attestation_count, deposit_count, exit_count, withdraw_count,
		withdraw_amount, attester_slashing_count, proposer_slashing_count, bls_change_count, eth_transaction_count, sync_participation, payload_count
	FROM unfinalized_epochs
	WHERE epoch = $1 AND epoch_head_root = $2
	`, epoch, headRoot)
	if err != nil {
		return nil
	}
	return &unfinalizedEpoch
}

func DeleteUnfinalizedEpochsBefore(epoch uint64, tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM unfinalized_epochs WHERE epoch < $1`, epoch)
	if err != nil {
		return err
	}
	return nil
}
