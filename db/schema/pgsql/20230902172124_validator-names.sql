-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."validator_names"
(
    "index" bigint NOT NULL,
    "name" character varying(250) NOT NULL,
    PRIMARY KEY ("index")
);

CREATE INDEX IF NOT EXISTS "validator_names_name_idx"
    ON public."validator_names" USING gin 
    ("name" gin_trgm_ops);

ALTER TABLE public."blocks" ALTER COLUMN "orphaned" TYPE smallint USING CASE
    WHEN "orphaned" THEN 1 ELSE 0
    END;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
