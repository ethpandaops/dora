-- +goose Up
-- +goose StatementBegin
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
DROP TABLE IF EXISTS public."unfinalized_metadata";
ALTER TABLE public."unfinalized_blocks" DROP COLUMN "metadata";
-- +goose StatementEnd
