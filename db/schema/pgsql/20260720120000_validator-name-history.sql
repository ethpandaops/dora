-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."validator_name_snapshots"
(
    "start_slot" bigint NOT NULL,
    "end_slot" bigint NOT NULL,
    "start_time" bigint NOT NULL,
    "end_time" bigint NOT NULL,
    "ranges_hash" character varying(64) NOT NULL,
    PRIMARY KEY ("start_slot")
);

CREATE TABLE IF NOT EXISTS public."validator_name_ranges"
(
    "snapshot_slot" bigint NOT NULL,
    "start_index" bigint NOT NULL,
    "end_index" bigint NOT NULL,
    "name" character varying(250) NOT NULL,
    PRIMARY KEY ("snapshot_slot", "start_index")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
