-- +goose Up
-- +goose StatementBegin
ALTER TABLE "epochs" ADD COLUMN "voted_target_slashed" bigint NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_epochs" ADD COLUMN "voted_target_slashed" bigint NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_epochs" ADD COLUMN "voted_target_slashed" bigint NOT NULL DEFAULT 0;
ALTER TABLE "slots" ADD COLUMN "builder_payment_weight" bigint NOT NULL DEFAULT 0;
ALTER TABLE "slots" ADD COLUMN "builder_payment_percent" real NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "epochs" DROP COLUMN "voted_target_slashed";
ALTER TABLE "unfinalized_epochs" DROP COLUMN "voted_target_slashed";
ALTER TABLE "orphaned_epochs" DROP COLUMN "voted_target_slashed";
ALTER TABLE "slots" DROP COLUMN "builder_payment_weight";
ALTER TABLE "slots" DROP COLUMN "builder_payment_percent";
-- +goose StatementEnd
