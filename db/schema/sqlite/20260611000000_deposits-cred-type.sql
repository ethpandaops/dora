-- +goose Up
-- +goose StatementBegin

ALTER TABLE "deposits"
ADD "cred_type" INTEGER NOT NULL DEFAULT 0;

UPDATE "deposits"
SET "cred_type" = (instr('123456789ABCDEF', substr(hex("withdrawalcredentials"), 1, 1)) * 16)
    + instr('123456789ABCDEF', substr(hex("withdrawalcredentials"), 2, 1))
WHERE length("withdrawalcredentials") > 0;

CREATE INDEX IF NOT EXISTS "deposits_cred_type_slot_number_idx"
    ON "deposits"
    ("cred_type" ASC, "slot_number" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
