-- +goose Up
-- +goose StatementBegin
ALTER TABLE public."ens_names" ADD COLUMN "network" TEXT NOT NULL DEFAULT '';
ALTER TABLE public."ens_names" DROP CONSTRAINT "ens_names_pkey";
ALTER TABLE public."ens_names" ADD CONSTRAINT "ens_names_pkey" PRIMARY KEY ("address", "network");
CREATE INDEX IF NOT EXISTS "ens_names_name_idx" ON public."ens_names" ("name") WHERE "name" != '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS "ens_names_name_idx";
DELETE FROM public."ens_names" WHERE "network" != '';
ALTER TABLE public."ens_names" DROP CONSTRAINT "ens_names_pkey";
ALTER TABLE public."ens_names" ADD CONSTRAINT "ens_names_pkey" PRIMARY KEY ("address");
ALTER TABLE public."ens_names" DROP COLUMN "network";
-- +goose StatementEnd
