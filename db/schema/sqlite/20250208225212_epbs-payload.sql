-- +goose Up
-- +goose StatementBegin

ALTER TABLE "unfinalized_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "unfinalized_blocks" ADD "payload_ssz" BLOB NULL;

ALTER TABLE "orphaned_blocks" ADD "payload_ver" int NOT NULL DEFAULT 0;
ALTER TABLE "orphaned_blocks" ADD "payload_ssz" BLOB NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd