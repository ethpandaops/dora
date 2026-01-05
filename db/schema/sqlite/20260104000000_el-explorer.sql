-- +goose Up
-- +goose StatementBegin

-- Add block_uid to slots table (slot << 16 | unique_index)
ALTER TABLE "slots" ADD COLUMN block_uid INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "slots_block_uid_idx"
    ON "slots"
    ("block_uid" ASC);

-- Add block_uid to unfinalized_blocks table
ALTER TABLE "unfinalized_blocks" ADD COLUMN block_uid INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "unfinalized_blocks_block_uid_idx"
    ON "unfinalized_blocks"
    ("block_uid" ASC);

-- Table for EL block sync status
CREATE TABLE IF NOT EXISTS "el_blocks" (
    block_uid INTEGER NOT NULL,
    status INTEGER NOT NULL DEFAULT 0,
    events INTEGER NOT NULL DEFAULT 0,
    transactions INTEGER NOT NULL DEFAULT 0,
    transfers INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT el_blocks_pkey PRIMARY KEY (block_uid)
);

-- Table for EL transactions (block_uid in primary key for orphan handling)
CREATE TABLE IF NOT EXISTS "el_transactions" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    tx_from BLOB NOT NULL,
    tx_to BLOB NULL,
    reverted INTEGER NOT NULL DEFAULT 0,
    amount BLOB NOT NULL,
    data BLOB NULL,
    gas_used INTEGER NOT NULL,
    block_number INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT el_transactions_pkey PRIMARY KEY (block_uid, tx_hash)
);

CREATE INDEX IF NOT EXISTS "el_transactions_block_uid_idx"
    ON "el_transactions"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_transactions_tx_hash_idx"
    ON "el_transactions"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "el_transactions_from_idx"
    ON "el_transactions"
    ("tx_from" ASC);

CREATE INDEX IF NOT EXISTS "el_transactions_to_idx"
    ON "el_transactions"
    ("tx_to" ASC);

-- Table for EL transaction events (logs)
CREATE TABLE IF NOT EXISTS "el_tx_events" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    event_index INTEGER NOT NULL,
    source BLOB NOT NULL,
    topic1 BLOB NOT NULL,
    topic2 BLOB NULL,
    topic3 BLOB NULL,
    topic4 BLOB NULL,
    topic5 BLOB NULL,
    data BLOB NULL,
    CONSTRAINT el_tx_events_pkey PRIMARY KEY (block_uid, tx_hash, event_index)
);

CREATE INDEX IF NOT EXISTS "el_tx_events_block_uid_idx"
    ON "el_tx_events"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_tx_events_tx_hash_idx"
    ON "el_tx_events"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "el_tx_events_topic1_idx"
    ON "el_tx_events"
    ("topic1" ASC);

-- Table for EL accounts
CREATE TABLE IF NOT EXISTS "el_accounts" (
    address BLOB NOT NULL,
    funder BLOB NULL,
    funded INTEGER NOT NULL DEFAULT 0,
    is_contract INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT el_accounts_pkey PRIMARY KEY (address)
);

-- Table for detected tokens
CREATE TABLE IF NOT EXISTS "el_tokens" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    contract BLOB NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    symbol TEXT NOT NULL DEFAULT '',
    decimals INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS "el_tokens_contract_idx"
    ON "el_tokens"
    ("contract" ASC);

-- Table for per-wallet balances per token
CREATE TABLE IF NOT EXISTS "el_balances" (
    account BLOB NOT NULL,
    token_id INTEGER NOT NULL,
    balance REAL NOT NULL DEFAULT 0,
    balance_raw BLOB NOT NULL,
    updated INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT el_balances_pkey PRIMARY KEY (account, token_id)
);

CREATE INDEX IF NOT EXISTS "el_balances_account_idx"
    ON "el_balances"
    ("account" ASC);

CREATE INDEX IF NOT EXISTS "el_balances_token_id_idx"
    ON "el_balances"
    ("token_id" ASC);

-- Table for token transfers
CREATE TABLE IF NOT EXISTS "el_token_transfers" (
    block_uid INTEGER NOT NULL,
    tx_hash BLOB NOT NULL,
    tx_idx INTEGER NOT NULL,
    token_id INTEGER NOT NULL,
    tx_from BLOB NOT NULL,
    tx_to BLOB NOT NULL,
    amount REAL NOT NULL DEFAULT 0,
    amount_raw BLOB NOT NULL,
    CONSTRAINT el_token_transfers_pkey PRIMARY KEY (block_uid, tx_hash, tx_idx)
);

CREATE INDEX IF NOT EXISTS "el_token_transfers_block_uid_idx"
    ON "el_token_transfers"
    ("block_uid" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_tx_hash_idx"
    ON "el_token_transfers"
    ("tx_hash" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_token_id_idx"
    ON "el_token_transfers"
    ("token_id" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_from_idx"
    ON "el_token_transfers"
    ("tx_from" ASC);

CREATE INDEX IF NOT EXISTS "el_token_transfers_to_idx"
    ON "el_token_transfers"
    ("tx_to" ASC);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
SELECT 'NOT SUPPORTED';
-- +goose StatementEnd
