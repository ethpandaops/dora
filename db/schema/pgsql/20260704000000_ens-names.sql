-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public."ens_names" (
    address bytea NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    resolved_time BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT ens_names_pkey PRIMARY KEY (address)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public."ens_names";
-- +goose StatementEnd
