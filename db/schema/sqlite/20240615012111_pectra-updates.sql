-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS consolidations (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root BLOB NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    source_index BIGINT NOT NULL,
    target_index BIGINT NOT NULL,
    epoch BIGINT NOT NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "consolidations_source_idx"
    ON "consolidations"
    ("source_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_target_idx"
    ON "consolidations"
    ("target_index" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_epoch_idx"
    ON "consolidations"
    ("epoch" ASC);

CREATE INDEX IF NOT EXISTS "consolidations_slot_idx"
    ON "consolidations"
    ("slot_number" ASC);

CREATE TABLE IF NOT EXISTS el_requests (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root BLOB NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    request_type INT NOT NULL,
    source_address BLOB NULL,
    source_index BIGINT NULL,
    source_pubkey BLOB NULL,
    target_index BIGINT NULL,
    target_pubkey BLOB NULL,
    amount BIGINT NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "el_requests_slot_idx"
    ON "el_requests"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_reqtype_idx"
    ON "el_requests"
    ("request_type" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_sourceaddr_idx"
    ON "el_requests"
    ("source_address" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_sourceidx_idx"
    ON "el_requests"
    ("source_index" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_targetidx_idx"
    ON "el_requests"
    ("target_index" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_sourcekey_idx"
    ON "el_requests"
    ("source_pubkey" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_targetkey_idx"
    ON "el_requests"
    ("target_pubkey" ASC);

CREATE INDEX IF NOT EXISTS "el_requests_amount_idx"
    ON "el_requests"
    ("amount" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
