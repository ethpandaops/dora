-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."orphaned_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."slots" ADD
 "payload_status" smallint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS "slots_payload_status_idx"
    ON public."slots" 
    ("payload_status" ASC NULLS LAST);

ALTER TABLE public."epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd