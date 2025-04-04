-- +goose Up
-- +goose StatementBegin

UPDATE "consolidation_requests"
    SET "result" = 1 WHERE "result" = 32;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
