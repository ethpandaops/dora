-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."sync_assignments"
(
    "period" bigint NOT NULL,
    "index" int NOT NULL,
    "validator" bigint NOT NULL,
    CONSTRAINT "sync_assignments_pkey" PRIMARY KEY ("period", "index")
);

CREATE INDEX IF NOT EXISTS "sync_assignments_validator_idx"
    ON public."sync_assignments" 
    ("validator" ASC NULLS LAST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
