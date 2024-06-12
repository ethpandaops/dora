-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS voluntary_exits (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root bytea NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    validator BIGINT NOT NULL,
    CONSTRAINT voluntary_exits_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "voluntary_exits_validator_idx"
    ON public."voluntary_exits"
    ("validator" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "voluntary_exits_slot_number_idx"
    ON public."voluntary_exits"
    ("slot_number" ASC NULLS FIRST);

CREATE TABLE IF NOT EXISTS slashings (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root bytea NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    validator BIGINT NOT NULL,
    slasher BIGINT NOT NULL,
    reason INT NOT NULL,
    CONSTRAINT slashings_pkey PRIMARY KEY (slot_root, slot_index, validator)
);

CREATE INDEX IF NOT EXISTS "slashings_slot_number_idx"
    ON public."slashings"
    ("slot_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "slashings_reason_slot_number_idx"
    ON public."slashings"
    (
        "reason" ASC NULLS FIRST,
        "slot_number" ASC NULLS FIRST
    );

CREATE INDEX IF NOT EXISTS "slashings_validator_idx"
    ON public."slashings"
    ("validator" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "slashings_slasher_idx"
    ON public."slashings"
    ("slasher" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
