package db

import (
	"fmt"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
)

// InsertValidator inserts a single validator into the database
func InsertValidator(validator *dbtypes.Validator, tx *sqlx.Tx) error {
	_, err := tx.Exec(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO validators (
				validator_index, pubkey, withdrawal_credentials, effective_balance,
				slashed, activation_eligibility_epoch, activation_epoch,
				exit_epoch, withdrawable_epoch
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (validator_index) DO UPDATE SET
				withdrawal_credentials = excluded.withdrawal_credentials,
				effective_balance = excluded.effective_balance,
				slashed = excluded.slashed,
				activation_eligibility_epoch = excluded.activation_eligibility_epoch,
				activation_epoch = excluded.activation_epoch,
				exit_epoch = excluded.exit_epoch,
				withdrawable_epoch = excluded.withdrawable_epoch`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO validators (
				validator_index, pubkey, withdrawal_credentials, effective_balance,
				slashed, activation_eligibility_epoch, activation_epoch,
				exit_epoch, withdrawable_epoch
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
	}),
		validator.ValidatorIndex,
		validator.Pubkey,
		validator.WithdrawalCredentials,
		validator.EffectiveBalance,
		validator.Slashed,
		validator.ActivationEligibilityEpoch,
		validator.ActivationEpoch,
		validator.ExitEpoch,
		validator.WithdrawableEpoch)

	if err != nil {
		return fmt.Errorf("error inserting validator: %v", err)
	}
	return nil
}

// InsertValidatorBatch inserts multiple validators in a batch
func InsertValidatorBatch(validators []*dbtypes.Validator, tx *sqlx.Tx) error {
	if len(validators) == 0 {
		return nil
	}

	valueStrings := make([]string, len(validators))
	valueArgs := make([]interface{}, 0, len(validators)*9)
	for i, val := range validators {
		valueStrings[i] = "($1, $2, $3, $4, $5, $6, $7, $8, $9)"
		valueArgs = append(valueArgs,
			val.ValidatorIndex,
			val.Pubkey,
			val.WithdrawalCredentials,
			val.EffectiveBalance,
			val.Slashed,
			val.ActivationEligibilityEpoch,
			val.ActivationEpoch,
			val.ExitEpoch,
			val.WithdrawableEpoch)
	}

	stmt := fmt.Sprintf(EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			INSERT INTO validators (
				validator_index, pubkey, withdrawal_credentials, effective_balance,
				slashed, activation_eligibility_epoch, activation_epoch,
				exit_epoch, withdrawable_epoch
			) VALUES %s
			ON CONFLICT (validator_index) DO UPDATE SET
				withdrawal_credentials = excluded.withdrawal_credentials,
				effective_balance = excluded.effective_balance,
				slashed = excluded.slashed,
				activation_eligibility_epoch = excluded.activation_eligibility_epoch,
				activation_epoch = excluded.activation_epoch,
				exit_epoch = excluded.exit_epoch,
				withdrawable_epoch = excluded.withdrawable_epoch`,
		dbtypes.DBEngineSqlite: `
			INSERT OR REPLACE INTO validators (
				validator_index, pubkey, withdrawal_credentials, effective_balance,
				slashed, activation_eligibility_epoch, activation_epoch,
				exit_epoch, withdrawable_epoch
			) VALUES %s`,
	}), strings.Join(valueStrings, ","))

	_, err := tx.Exec(stmt, valueArgs...)
	if err != nil {
		return fmt.Errorf("error inserting validator batch: %v", err)
	}

	return nil
}

// GetValidatorByIndex returns a validator by index
func GetValidatorByIndex(index phase0.ValidatorIndex) *dbtypes.Validator {
	validator := dbtypes.Validator{}
	err := ReaderDb.Get(&validator, `
		SELECT * FROM validators WHERE validator_index = $1
	`, index)
	if err != nil {
		return nil
	}
	return &validator
}

// GetValidatorByPubkey returns a validator by pubkey
func GetValidatorByPubkey(pubkey []byte) *dbtypes.Validator {
	validator := dbtypes.Validator{}
	err := ReaderDb.Get(&validator, `
		SELECT * FROM validators WHERE pubkey = $1
	`, pubkey)
	if err != nil {
		return nil
	}
	return &validator
}

// GetValidatorRange returns validators in a given index range
func GetValidatorRange(startIndex uint64, endIndex uint64) []*dbtypes.Validator {
	validators := []*dbtypes.Validator{}
	err := ReaderDb.Select(&validators, `
		SELECT * FROM validators 
		WHERE validator_index >= $1 AND validator_index <= $2
		ORDER BY validator_index ASC
	`, startIndex, endIndex)
	if err != nil {
		logger.Errorf("Error while fetching validator range: %v", err)
		return nil
	}
	return validators
}

// GetMaxValidatorIndex returns the highest validator index in the database
func GetMaxValidatorIndex() (uint64, error) {
	var maxIndex uint64
	err := ReaderDb.Get(&maxIndex, "SELECT MAX(validator_index) FROM validators")
	if err != nil {
		return 0, fmt.Errorf("error getting max validator index: %v", err)
	}
	return maxIndex, nil
}
