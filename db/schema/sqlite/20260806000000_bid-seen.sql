-- +goose Up
-- +goose StatementBegin
ALTER TABLE "block_bids" ADD "seen_count" int NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "block_bids" ADD "seen_total" int NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "block_bids" DROP COLUMN "seen_count";
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE "block_bids" DROP COLUMN "seen_total";
-- +goose StatementEnd
