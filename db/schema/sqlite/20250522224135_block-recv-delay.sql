-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots"
    ADD "recv_delay" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_blocks"
    ADD "recv_delay" integer NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd 