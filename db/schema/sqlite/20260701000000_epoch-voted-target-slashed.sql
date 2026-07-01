-- +goose Up
-- +goose StatementBegin
ALTER TABLE "epochs" ADD COLUMN "voted_target_slashed" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_epochs" ADD COLUMN "voted_target_slashed" INTEGER NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_epochs" ADD COLUMN "voted_target_slashed" INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "epochs" DROP COLUMN "voted_target_slashed";
ALTER TABLE "unfinalized_epochs" DROP COLUMN "voted_target_slashed";
ALTER TABLE "orphaned_epochs" DROP COLUMN "voted_target_slashed";
-- +goose StatementEnd
