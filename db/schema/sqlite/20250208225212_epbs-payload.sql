-- +goose Up
-- +goose StatementBegin

ALTER TABLE "unfinalized_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "orphaned_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "slots" ADD "payload_status" smallint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS "slots_payload_status_idx" ON "slots" ("payload_status" ASC);

ALTER TABLE "epochs" ADD "payload_count" int NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_epochs" ADD "payload_count" int NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd