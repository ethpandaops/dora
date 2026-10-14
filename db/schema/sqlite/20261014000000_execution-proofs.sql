-- +goose Up
-- +goose StatementBegin
ALTER TABLE "slots" ADD COLUMN "proof_count" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "slots" DROP COLUMN "proof_count";
-- +goose StatementEnd
