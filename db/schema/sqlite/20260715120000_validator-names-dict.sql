-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "validator_names_dict"
(
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "name" character varying(250) NOT NULL,
    UNIQUE ("name")
);

ALTER TABLE "slots" ADD COLUMN "proposer_name_id" bigint;

CREATE INDEX IF NOT EXISTS "slots_proposer_name_id_idx"
    ON "slots"
    ("proposer_name_id" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
