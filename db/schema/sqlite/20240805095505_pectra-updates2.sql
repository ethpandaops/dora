-- +goose Up
-- +goose StatementBegin

DROP TABLE IF EXISTS "consolidations";

CREATE TABLE IF NOT EXISTS "consolidations" (
    slot_number INT NOT NULL,
    slot_root BLOB NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address BLOB NOT NULL,
    source_index BIGINT NULL,
    source_pubkey BLOB NULL,
    target_index BIGINT NULL,
    target_pubkey BLOB NULL,
    tx_hash BLOB NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "consolidations_slot_idx"
    ON "consolidations"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_source_idx"
    ON "consolidations"
    ("source_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_target_idx"
    ON "consolidations"
    ("target_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_source_addr_idx"
    ON "consolidations"
    ("source_address" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_fork_idx"
    ON "consolidations"
    ("fork_idx" ASC);


DROP TABLE IF EXISTS "el_requests";

CREATE TABLE IF NOT EXISTS "withdrawal_requests" (
    slot_number INT NOT NULL,
    slot_root BLOB NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address BLOB NOT NULL,
    validator_index BIGINT NULL,
    validator_pubkey BLOB NOT NULL,
    amount BIGINT NOT NULL,
    tx_hash BLOB NULL,
    CONSTRAINT withdrawal_requests_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_slot_idx"
    ON "withdrawal_requests"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_validator_idx"
    ON "withdrawal_requests"
    ("validator_index" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_source_addr_idx"
    ON "withdrawal_requests"
    ("source_address" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_fork_idx"
    ON "withdrawal_requests"
    ("fork_idx" ASC);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_amount_idx"
    ON "withdrawal_requests"
    ("amount" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
