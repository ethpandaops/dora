-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS "ens_names" (
    address BLOB NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    resolved_time INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ens_names_pkey PRIMARY KEY (address)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS "ens_names";
-- +goose StatementEnd
