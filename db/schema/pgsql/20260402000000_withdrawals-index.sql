-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public."withdrawals" (
    block_uid BIGINT NOT NULL,
    block_idx SMALLINT NOT NULL DEFAULT 0,
    type SMALLINT NOT NULL DEFAULT 0,
    orphaned bool NOT NULL DEFAULT FALSE,
    fork_id BIGINT NOT NULL DEFAULT 0,
    validator BIGINT NULL,
    account_id BIGINT NULL,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    amount_raw bytea NOT NULL,
    CONSTRAINT withdrawals_pkey PRIMARY KEY (block_uid, block_idx)
);

CREATE INDEX IF NOT EXISTS "withdrawals_block_uid_idx" ON public."withdrawals" ("block_uid" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_validator_idx" ON public."withdrawals" ("validator" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_account_id_idx" ON public."withdrawals" ("account_id" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_type_idx" ON public."withdrawals" ("type" ASC NULLS FIRST);
CREATE INDEX IF NOT EXISTS "withdrawals_orphaned_idx" ON public."withdrawals" ("orphaned");

INSERT INTO withdrawals (block_uid, block_idx, type, orphaned, fork_id, validator, account_id, amount, amount_raw)
    SELECT block_uid, block_index, type, FALSE, 0, validator, account_id, amount, amount_raw
    FROM el_withdrawals;

DROP TABLE IF EXISTS public."el_withdrawals";

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public."withdrawals";
-- +goose StatementEnd
