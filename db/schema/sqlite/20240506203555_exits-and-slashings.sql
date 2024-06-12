-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS voluntary_exits (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root BLOB NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    validator BIGINT NOT NULL,
    CONSTRAINT voluntary_exits_pkey PRIMARY KEY (slot_root, slot_index)
);

CREATE INDEX IF NOT EXISTS "voluntary_exits_validator_idx"
    ON "voluntary_exits"
    ("validator" ASC);

CREATE INDEX IF NOT EXISTS "voluntary_exits_slot_number_idx"
    ON "voluntary_exits"
    ("slot_number" ASC);

CREATE TABLE IF NOT EXISTS slashings (
    slot_number INT NOT NULL,
    slot_index INT NOT NULL,
    slot_root BLOB NOT NULL,
    orphaned bool NOT NULL DEFAULT FALSE,
    validator BIGINT NOT NULL,
    slasher BIGINT NOT NULL,
    reason INT NOT NULL,
    CONSTRAINT slashings_pkey PRIMARY KEY (slot_root, slot_index, validator)
);

CREATE INDEX IF NOT EXISTS "slashings_slot_number_idx"
    ON "slashings"
    ("slot_number" ASC);

CREATE INDEX IF NOT EXISTS "slashings_reason_slot_number_idx"
    ON "slashings"
    (
        "reason" ASC,
        "slot_number" ASC
    );

CREATE INDEX IF NOT EXISTS "slashings_validator_idx"
    ON "slashings"
    ("validator" ASC);

CREATE INDEX IF NOT EXISTS "slashings_slasher_idx"
    ON "slashings"
    ("slasher" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
