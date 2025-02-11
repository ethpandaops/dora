-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."consolidation_requests"
    ADD "result" TINYINT NOT NULL DEFAULT 0;

ALTER TABLE public."withdrawal_requests"
    ADD "result" TINYINT NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
