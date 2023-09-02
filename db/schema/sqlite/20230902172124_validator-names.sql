-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "validator_names"
(
    "index" bigint NOT NULL,
    "name" character varying(250) NOT NULL,
    PRIMARY KEY ("index")
);

CREATE INDEX IF NOT EXISTS "validator_names_name_idx"
    ON "validator_names" 
    ("name" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
