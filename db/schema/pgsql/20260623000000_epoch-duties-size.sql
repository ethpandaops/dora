-- +goose Up
-- +goose StatementBegin
ALTER TABLE "epochs" ADD COLUMN "block_duties_size" bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "epochs" DROP COLUMN "block_duties_size";
-- +goose StatementEnd
