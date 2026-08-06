-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."block_bids"
 ADD COLUMN "seen_count" int NOT NULL DEFAULT 0,
 ADD COLUMN "seen_total" int NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE public."block_bids"
 DROP COLUMN "seen_count",
 DROP COLUMN "seen_total";

-- +goose StatementEnd
