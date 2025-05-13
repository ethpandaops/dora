-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."slots"
ADD "block_size" integer NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 