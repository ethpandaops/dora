-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."slots"
    ADD "min_exec_time" integer NOT NULL DEFAULT 0,
    ADD "max_exec_time" integer NOT NULL DEFAULT 0,
    ADD "exec_times" bytea;

ALTER TABLE public."unfinalized_blocks"
    ADD "min_exec_time" integer NOT NULL DEFAULT 0,
    ADD "max_exec_time" integer NOT NULL DEFAULT 0,
    ADD "exec_times" bytea;

-- Add index on execution time fields for performance queries
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_slots_exec_times" ON public."slots" ("min_exec_time", "max_exec_time") WHERE "min_exec_time" > 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd