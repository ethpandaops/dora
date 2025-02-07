-- +goose Up
-- +goose StatementBegin

ALTER TABLE "consolidation_requests"
    ADD "result" TINYINT NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
