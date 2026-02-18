-- +goose Up
-- +goose StatementBegin

-- Add block_index column to el_withdrawals for distinguishing multiple
-- withdrawals to the same address within a single block.
ALTER TABLE public."el_withdrawals" ADD COLUMN IF NOT EXISTS block_index SMALLINT NOT NULL DEFAULT 0;

-- Assign sequential block_index values per block_uid for existing rows
UPDATE public."el_withdrawals" AS w
SET block_index = sub.rn
FROM (
    SELECT block_uid, account_id, type,
           (ROW_NUMBER() OVER (PARTITION BY block_uid ORDER BY type, account_id) - 1) AS rn
    FROM public."el_withdrawals"
) AS sub
WHERE w.block_uid = sub.block_uid
  AND w.account_id = sub.account_id
  AND w.type = sub.type;

-- Replace the old primary key (block_uid, account_id, type) with (block_uid, block_index)
ALTER TABLE public."el_withdrawals" DROP CONSTRAINT IF EXISTS el_withdrawals_pkey;
ALTER TABLE public."el_withdrawals" ADD CONSTRAINT el_withdrawals_pkey PRIMARY KEY (block_uid, block_index);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
