-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."deposits"
ADD "cred_type" SMALLINT NOT NULL DEFAULT 0;

UPDATE public."deposits"
SET "cred_type" = get_byte("withdrawalcredentials", 0)
WHERE length("withdrawalcredentials") > 0;

CREATE INDEX IF NOT EXISTS "deposits_cred_type_slot_number_idx"
    ON public."deposits"
    ("cred_type" ASC, "slot_number" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
