-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE "epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 