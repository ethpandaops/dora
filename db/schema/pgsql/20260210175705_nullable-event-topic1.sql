-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."el_tx_events"
ALTER COLUMN "topic1" DROP NOT NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
