-- +goose Up
-- +goose StatementBegin

-- Add block_uid to slots table (slot << 16 | unique_index)
ALTER TABLE public."slots" ADD COLUMN IF NOT EXISTS block_uid BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "slots_block_uid_idx"
    ON public."slots"
    ("block_uid" ASC NULLS FIRST);

-- Add block_uid to unfinalized_blocks table
ALTER TABLE public."unfinalized_blocks" ADD COLUMN IF NOT EXISTS block_uid BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "unfinalized_blocks_block_uid_idx"
    ON public."unfinalized_blocks"
    ("block_uid" ASC NULLS FIRST);

-- Add block_uid to orphaned_blocks table
ALTER TABLE public."orphaned_blocks" ADD COLUMN IF NOT EXISTS block_uid BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "orphaned_blocks_block_uid_idx"
    ON public."orphaned_blocks"
    ("block_uid" ASC NULLS FIRST);

-- Table for EL block sync status
CREATE TABLE IF NOT EXISTS public."el_blocks" (
    block_uid BIGINT NOT NULL,
    status INT NOT NULL DEFAULT 0,
    events INT NOT NULL DEFAULT 0,
    transactions INT NOT NULL DEFAULT 0,
    transfers INT NOT NULL DEFAULT 0,
    CONSTRAINT el_blocks_pkey PRIMARY KEY (block_uid)
);

-- Table for EL transactions (block_uid in primary key for orphan handling)
CREATE TABLE IF NOT EXISTS public."el_transactions" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    tx_from bytea NOT NULL,
    tx_to bytea NULL,
    reverted bool NOT NULL DEFAULT FALSE,
    amount bytea NOT NULL,
    data bytea NULL,
    gas_used BIGINT NOT NULL,
    block_number BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT el_transactions_pkey PRIMARY KEY (block_uid, tx_hash)
);

CREATE INDEX IF NOT EXISTS "el_transactions_block_uid_idx"
    ON public."el_transactions"
    ("block_uid" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_transactions_tx_hash_idx"
    ON public."el_transactions"
    ("tx_hash" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_transactions_from_idx"
    ON public."el_transactions"
    ("tx_from" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_transactions_to_idx"
    ON public."el_transactions"
    ("tx_to" ASC NULLS FIRST);

-- Table for EL transaction events (logs)
CREATE TABLE IF NOT EXISTS public."el_tx_events" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    event_index INT NOT NULL,
    source bytea NOT NULL,
    topic1 bytea NOT NULL,
    topic2 bytea NULL,
    topic3 bytea NULL,
    topic4 bytea NULL,
    topic5 bytea NULL,
    data bytea NULL,
    CONSTRAINT el_tx_events_pkey PRIMARY KEY (block_uid, tx_hash, event_index)
);

CREATE INDEX IF NOT EXISTS "el_tx_events_block_uid_idx"
    ON public."el_tx_events"
    ("block_uid" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_tx_events_tx_hash_idx"
    ON public."el_tx_events"
    ("tx_hash" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_tx_events_topic1_idx"
    ON public."el_tx_events"
    ("topic1" ASC NULLS FIRST);

-- Table for EL accounts
CREATE TABLE IF NOT EXISTS public."el_accounts" (
    address bytea NOT NULL,
    funder bytea NULL,
    funded BIGINT NOT NULL DEFAULT 0,
    is_contract bool NOT NULL DEFAULT FALSE,
    CONSTRAINT el_accounts_pkey PRIMARY KEY (address)
);

-- Table for detected tokens
CREATE TABLE IF NOT EXISTS public."el_tokens" (
    id BIGSERIAL NOT NULL,
    contract bytea NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    symbol TEXT NOT NULL DEFAULT '',
    decimals SMALLINT NOT NULL DEFAULT 0,
    CONSTRAINT el_tokens_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS "el_tokens_contract_idx"
    ON public."el_tokens"
    ("contract" ASC NULLS FIRST);

-- Table for per-wallet balances per token
CREATE TABLE IF NOT EXISTS public."el_balances" (
    account bytea NOT NULL,
    token_id BIGINT NOT NULL,
    balance DOUBLE PRECISION NOT NULL DEFAULT 0,
    balance_raw bytea NOT NULL,
    updated BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT el_balances_pkey PRIMARY KEY (account, token_id)
);

CREATE INDEX IF NOT EXISTS "el_balances_account_idx"
    ON public."el_balances"
    ("account" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_balances_token_id_idx"
    ON public."el_balances"
    ("token_id" ASC NULLS FIRST);

-- Table for token transfers
CREATE TABLE IF NOT EXISTS public."el_token_transfers" (
    block_uid BIGINT NOT NULL,
    tx_hash bytea NOT NULL,
    tx_idx INT NOT NULL,
    token_id BIGINT NOT NULL,
    token_type SMALLINT NOT NULL DEFAULT 0,
    token_index bytea NULL,
    tx_from bytea NOT NULL,
    tx_to bytea NOT NULL,
    amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    amount_raw bytea NOT NULL,
    CONSTRAINT el_token_transfers_pkey PRIMARY KEY (block_uid, tx_hash, tx_idx)
);

CREATE INDEX IF NOT EXISTS "el_token_transfers_block_uid_idx"
    ON public."el_token_transfers"
    ("block_uid" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_tx_hash_idx"
    ON public."el_token_transfers"
    ("tx_hash" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_token_id_idx"
    ON public."el_token_transfers"
    ("token_id" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_token_type_idx"
    ON public."el_token_transfers"
    ("token_type" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_token_index_idx"
    ON public."el_token_transfers"
    ("token_index" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_from_idx"
    ON public."el_token_transfers"
    ("tx_from" ASC NULLS FIRST);

CREATE INDEX IF NOT EXISTS "el_token_transfers_to_idx"
    ON public."el_token_transfers"
    ("tx_to" ASC NULLS FIRST);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
