-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS "sync_assignments"
(
    "period" bigint NOT NULL,
    "index" int NOT NULL,
    "validator" bigint NOT NULL,
    CONSTRAINT "sync_assignments_pkey" PRIMARY KEY ("period", "index")
);

CREATE INDEX IF NOT EXISTS "sync_assignments_validator_idx"
    ON "sync_assignments" 
    ("validator" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
