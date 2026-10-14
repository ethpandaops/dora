-- +goose Up
-- +goose StatementBegin
ALTER TABLE "slots" ADD COLUMN "proof_count" INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS "unfinalized_metadata"
(
    "slot" INTEGER NOT NULL,
    "kind" INTEGER NOT NULL,
    "key" BLOB NOT NULL,
    "data" BLOB NOT NULL,
    CONSTRAINT "unfinalized_metadata_pkey" PRIMARY KEY ("slot", "kind", "key")
);

ALTER TABLE "unfinalized_blocks" ADD COLUMN "metadata" BLOB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "unfinalized_blocks" DROP COLUMN "metadata";
DROP TABLE IF EXISTS "unfinalized_metadata";
ALTER TABLE "slots" DROP COLUMN "proof_count";
-- +goose StatementEnd
