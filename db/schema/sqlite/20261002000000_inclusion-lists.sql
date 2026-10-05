-- +goose Up
-- +goose StatementBegin
ALTER TABLE "slots" ADD COLUMN "il_count" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "slots" ADD COLUMN "il_unsatisfied" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "slots" DROP COLUMN "il_count";
ALTER TABLE "slots" DROP COLUMN "il_unsatisfied";
-- +goose StatementEnd
