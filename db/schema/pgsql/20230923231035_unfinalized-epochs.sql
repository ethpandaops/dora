-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."unfinalized_epochs"
(
    "epoch" bigint NOT NULL,
    "validator_count" bigint NOT NULL DEFAULT 0,
    "validator_balance" bigint NOT NULL DEFAULT 0,
    "eligible" bigint NOT NULL DEFAULT 0,
    "voted_target" bigint NOT NULL DEFAULT 0,
    "voted_head" bigint NOT NULL DEFAULT 0,
    "voted_total" bigint NOT NULL DEFAULT 0,
    "block_count" smallint NOT NULL DEFAULT 0,
    "orphaned_count" smallint NOT NULL DEFAULT 0,
    "attestation_count" integer NOT NULL DEFAULT 0,
    "deposit_count" integer NOT NULL DEFAULT 0,
    "exit_count" integer NOT NULL DEFAULT 0,
    "withdraw_count" integer NOT NULL DEFAULT 0,
    "withdraw_amount" bigint NOT NULL DEFAULT 0,
    "attester_slashing_count" integer NOT NULL DEFAULT 0,
    "proposer_slashing_count" integer NOT NULL DEFAULT 0,
    "bls_change_count" integer NOT NULL DEFAULT 0,
    "eth_transaction_count" integer NOT NULL DEFAULT 0,
    "sync_participation" real NOT NULL DEFAULT 0,
    CONSTRAINT "unfinalized_epochs_pkey" PRIMARY KEY ("epoch")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
