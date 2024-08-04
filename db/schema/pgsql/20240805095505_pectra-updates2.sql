-- +goose Up
-- +goose StatementBegin

DROP TABLE IF EXISTS public."consolidations";

CREATE TABLE IF NOT EXISTS public."consolidations" (
    slot_number INT NOT NULL,
    slot_root bytea NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    source_index BIGINT NULL,
    source_pubkey bytea NULL,
    target_index BIGINT NULL,
    target_pubkey bytea NULL,
    tx_hash bytea NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "consolidations_slot_idx"
    ON public."consolidations"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_source_idx"
    ON public."consolidations"
    ("source_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_target_idx"
    ON public."consolidations"
    ("target_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_source_addr_idx"
    ON public."consolidations"
    ("source_address" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_fork_idx"
    ON public."consolidations"
    ("fork_idx" ASC NULLS FIRST);


DROP TABLE IF EXISTS public."el_requests";

CREATE TABLE IF NOT EXISTS public."withdrawal_requests" (
    slot_number INT NOT NULL,
    slot_root bytea NOT NULL,
    slot_index INT NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    source_address bytea NOT NULL,
    validator_index BIGINT NULL,
    validator_pubkey bytea NOT NULL,
    amount BIGINT NOT NULL,
    tx_hash bytea NULL,
    CONSTRAINT withdrawal_requests_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_slot_idx"
    ON public."withdrawal_requests"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_validator_idx"
    ON public."withdrawal_requests"
    ("validator_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_source_addr_idx"
    ON public."withdrawal_requests"
    ("source_address" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_fork_idx"
    ON public."withdrawal_requests"
    ("fork_idx" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "withdrawal_requests_amount_idx"
    ON public."withdrawal_requests"
    ("amount" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
