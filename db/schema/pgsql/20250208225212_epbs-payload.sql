-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."unfinalized_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."orphaned_blocks" ADD
 "payload_ver" int NOT NULL DEFAULT 0,
 "payload_ssz" bytea NULL;

ALTER TABLE public."slots" ADD
 "has_payload" boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS "slots_has_payload_idx"
    ON public."slots" 
    ("has_payload" ASC NULLS LAST);

ALTER TABLE public."epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

ALTER TABLE public."unfinalized_epochs" ADD
 "payload_count" int NOT NULL DEFAULT 0;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd