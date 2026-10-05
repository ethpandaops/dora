-- +goose Up
-- +goose StatementBegin
ALTER TABLE "slots" ADD COLUMN "il_count" smallint NOT NULL DEFAULT 0;
ALTER TABLE "slots" ADD COLUMN "il_unsatisfied" smallint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "slots" DROP COLUMN "il_count";
ALTER TABLE "slots" DROP COLUMN "il_unsatisfied";
-- +goose StatementEnd
