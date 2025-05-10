-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."slots"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE public."epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_epochs"
ADD "blob_count" integer NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 