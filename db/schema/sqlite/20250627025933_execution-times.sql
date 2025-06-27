-- +goose Up
-- +goose StatementBegin

ALTER TABLE "slots"
    ADD "min_exec_time" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
    ADD "max_exec_time" integer NOT NULL DEFAULT 0;

ALTER TABLE "slots"
    ADD "exec_times" blob;

ALTER TABLE "unfinalized_blocks"
    ADD "min_exec_time" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_blocks"
    ADD "max_exec_time" integer NOT NULL DEFAULT 0;

ALTER TABLE "unfinalized_blocks"
    ADD "exec_times" blob;

-- Add index on execution time fields for performance queries
CREATE INDEX IF NOT EXISTS "idx_slots_exec_times" ON "slots" ("min_exec_time", "max_exec_time") WHERE "min_exec_time" > 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd