-- +goose Up
-- +goose StatementBegin

ALTER TABLE public."deposit_txs"
RENAME TO "deposit_txs_old";

CREATE TABLE IF NOT EXISTS deposit_txs (
    deposit_index BIGINT NOT NULL,
    block_number BIGINT NOT NULL,
    block_time BIGINT NOT NULL,
    block_root bytea NOT NULL,
    publickey bytea NOT NULL,
    withdrawalcredentials bytea NOT NULL,
    amount BIGINT NOT NULL,
    signature bytea NOT NULL,
    valid_signature smallint NOT NULL DEFAULT 1,
    orphaned bool NOT NULL DEFAULT FALSE,
    tx_hash bytea NOT NULL,
    tx_sender bytea NOT NULL,
    tx_target bytea NOT NULL,
    fork_id BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT deposit_txs_pkey PRIMARY KEY (deposit_index, block_root)
);

INSERT INTO public."deposit_txs" (
    deposit_index,
    block_number,
    block_time,
    block_root,
    publickey,
    withdrawalcredentials,
    amount,
    signature,
    valid_signature,
    orphaned,
    tx_hash,
    tx_sender,
    tx_target,
    fork_id
)
SELECT
    deposit_index,
    block_number,
    block_time,
    block_root,
    publickey,
    withdrawalcredentials,
    amount,
    signature,
    CASE WHEN valid_signature THEN 1 ELSE 0 END,
    orphaned,
    tx_hash,
    tx_sender,
    tx_target,
    fork_id
FROM public."deposit_txs_old";

DROP TABLE IF EXISTS public."deposit_txs_old";

CREATE INDEX IF NOT EXISTS "deposit_txs_deposit_index_idx"
    ON public."deposit_txs"
    ("deposit_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_block_number_idx"
    ON public."deposit_txs"
    ("block_number" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_publickey_idx"
    ON public."deposit_txs"
    ("publickey" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_sender_idx"
    ON public."deposit_txs"
    ("tx_sender" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_tx_target_idx"
    ON public."deposit_txs"
    ("tx_target" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_valid_signature_idx"
    ON public."deposit_txs"
    ("valid_signature" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "deposit_txs_fork_id_idx"
    ON public."deposit_txs"
    ("fork_id" ASC NULLS FIRST);

-- fix fork_id for canonical deposit txs
UPDATE deposit_txs SET fork_id = 0
WHERE deposit_index IN (
	SELECT deposit_index FROM deposit_txs
	LEFT JOIN forks ON forks.fork_id = deposit_txs.fork_id
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
