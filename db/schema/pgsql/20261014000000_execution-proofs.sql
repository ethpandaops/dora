-- +goose Up
-- +goose StatementBegin
ALTER TABLE "slots" ADD COLUMN "proof_count" smallint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS public."unfinalized_metadata"
(
    "slot" bigint NOT NULL,
    "kind" smallint NOT NULL,
    "key" bytea NOT NULL,
    "data" bytea NOT NULL,
    CONSTRAINT "unfinalized_metadata_pkey" PRIMARY KEY ("slot", "kind", "key")
);

ALTER TABLE public."unfinalized_blocks" ADD COLUMN "metadata" bytea;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public."unfinalized_blocks" DROP COLUMN "metadata";
DROP TABLE IF EXISTS public."unfinalized_metadata";
ALTER TABLE "slots" DROP COLUMN "proof_count";
-- +goose StatementEnd
