-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS consolidations (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root bytea NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    source_index BIGINT NOT NULL,
    target_index BIGINT NOT NULL,
    epoch BIGINT NOT NULL,
    CONSTRAINT consolidation_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "consolidations_source_idx"
    ON public."consolidations"
    ("source_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_target_idx"
    ON public."consolidations"
    ("target_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_epoch_idx"
    ON public."consolidations"
    ("epoch" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "consolidations_slot_idx"
    ON public."consolidations"
    ("slot_number" ASC NULLS FIRST);

CREATE TABLE IF NOT EXISTS el_requests (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root bytea NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    request_type INT NOT NULL,
    source_address bytea NULL,
    source_index BIGINT NULL,
    source_pubkey bytea NULL,
    target_index BIGINT NULL,
    target_pubkey bytea NULL,
    amount BIGINT NULL,
    CONSTRAINT el_requests_pkey PRIMARY KEY (slot_index, slot_root)
);

CREATE INDEX IF NOT EXISTS "el_requests_slot_idx"
    ON public."el_requests"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_reqtype_idx"
    ON public."el_requests"
    ("request_type" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_sourceaddr_idx"
    ON public."el_requests"
    ("source_address" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_sourceidx_idx"
    ON public."el_requests"
    ("source_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_targetidx_idx"
    ON public."el_requests"
    ("target_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_sourcekey_idx"
    ON public."el_requests"
    ("source_pubkey" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_targetkey_idx"
    ON public."el_requests"
    ("target_pubkey" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_requests_amount_idx"
    ON public."el_requests"
    ("amount" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
