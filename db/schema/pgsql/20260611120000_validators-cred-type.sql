-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."validators"
ADD "cred_type" SMALLINT NOT NULL DEFAULT 0;

UPDATE public."validators"
SET "cred_type" = get_byte("withdrawal_credentials", 0)
WHERE length("withdrawal_credentials") > 0;

CREATE INDEX IF NOT EXISTS "validators_cred_type_validator_index_idx"
    ON public."validators"
    ("cred_type" ASC, "validator_index" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
