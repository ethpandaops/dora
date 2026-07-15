-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."validator_name_history"
(
    "range_start" bigint NOT NULL,
    "range_end" bigint NOT NULL,
    "start_slot" bigint NOT NULL,
    "end_slot" bigint NOT NULL,
    "name" character varying(250) NOT NULL,
    PRIMARY KEY ("range_start", "start_slot")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
