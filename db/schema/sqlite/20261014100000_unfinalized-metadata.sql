-- +goose Up
-- +goose StatementBegin
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
DROP TABLE IF EXISTS "unfinalized_metadata";
ALTER TABLE "unfinalized_blocks" DROP COLUMN "metadata";
-- +goose StatementEnd
