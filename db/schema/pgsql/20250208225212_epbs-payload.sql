-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."orphaned_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd