-- +goose Up
-- +goose StatementBegin

DROP TABLE IF EXISTS "unfinalized_duties";

CREATE TABLE IF NOT EXISTS "unfinalized_epochs"
(
    "epoch" BIGINT NOT NULL,
    "validator_count" BIGINT NOT NULL DEFAULT 0,
    "validator_balance" BIGINT NOT NULL DEFAULT 0,
    "eligible" BIGINT NOT NULL DEFAULT 0,
    "voted_target" BIGINT NOT NULL DEFAULT 0,
    "voted_head" BIGINT NOT NULL DEFAULT 0,
    "voted_total" BIGINT NOT NULL DEFAULT 0,
    "block_count" INTEGER NOT NULL DEFAULT 0,
    "orphaned_count" INTEGER NOT NULL DEFAULT 0,
    "attestation_count" INTEGER NOT NULL DEFAULT 0,
    "deposit_count" INTEGER NOT NULL DEFAULT 0,
    "exit_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_count" INTEGER NOT NULL DEFAULT 0,
    "withdraw_amount" BIGINT NOT NULL DEFAULT 0,
    "attester_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "proposer_slashing_count" INTEGER NOT NULL DEFAULT 0,
    "bls_change_count" INTEGER NOT NULL DEFAULT 0,
    "eth_transaction_count" INTEGER NOT NULL DEFAULT 0,
    "sync_participation" REAL NOT NULL DEFAULT 0,
    CONSTRAINT "unfinalized_epochs_pkey" PRIMARY KEY ("epoch")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
