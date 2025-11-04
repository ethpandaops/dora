-- +goose Up
-- +goose StatementBegin

-- Execution Layer Explorer Tables

-- Address metadata and stats
CREATE TABLE IF NOT EXISTS "el_addresses" (
    "address" BLOB NOT NULL,
    "is_contract" INTEGER NOT NULL DEFAULT 0,
    "contract_creator" BLOB,
    "creation_tx_hash" BLOB,
    "creation_block_number" INTEGER,
    "creation_timestamp" INTEGER,

    -- Transaction stats
    "first_seen_block" INTEGER NOT NULL,
    "first_seen_timestamp" INTEGER NOT NULL,
    "last_seen_block" INTEGER NOT NULL,
    "last_seen_timestamp" INTEGER NOT NULL,
    "tx_count" INTEGER NOT NULL DEFAULT 0,
    "in_tx_count" INTEGER NOT NULL DEFAULT 0,
    "out_tx_count" INTEGER NOT NULL DEFAULT 0,

    -- Balance tracking
    "balance" BLOB NOT NULL DEFAULT X'00',
    "balance_updated_block" INTEGER NOT NULL DEFAULT 0,

    -- Naming
    "ens_name" TEXT,
    "ens_updated_at" INTEGER,
    "custom_name" TEXT,

    -- Contract metadata
    "contract_bytecode_hash" BLOB,
    "contract_verified" INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY ("address")
);

CREATE INDEX IF NOT EXISTS "el_addresses_is_contract_idx" ON "el_addresses" ("is_contract");
CREATE INDEX IF NOT EXISTS "el_addresses_tx_count_idx" ON "el_addresses" ("tx_count" DESC);
CREATE INDEX IF NOT EXISTS "el_addresses_balance_idx" ON "el_addresses" ("balance" DESC);
CREATE INDEX IF NOT EXISTS "el_addresses_ens_idx" ON "el_addresses" ("ens_name");

-- Execution layer blocks
CREATE TABLE IF NOT EXISTS "el_blocks" (
    "number" INTEGER NOT NULL,
    "hash" BLOB NOT NULL,
    "parent_hash" BLOB NOT NULL,
    "timestamp" INTEGER NOT NULL,
    "fee_recipient" BLOB NOT NULL,
    "state_root" BLOB NOT NULL,
    "receipts_root" BLOB NOT NULL,
    "logs_bloom" BLOB NOT NULL,

    "difficulty" BLOB,
    "total_difficulty" BLOB,
    "size" INTEGER NOT NULL,
    "gas_used" INTEGER NOT NULL,
    "gas_limit" INTEGER NOT NULL,
    "base_fee_per_gas" INTEGER,
    "extra_data" BLOB,
    "nonce" BLOB,

    "transaction_count" INTEGER NOT NULL DEFAULT 0,
    "internal_tx_count" INTEGER NOT NULL DEFAULT 0,
    "event_count" INTEGER NOT NULL DEFAULT 0,
    "withdrawal_count" INTEGER NOT NULL DEFAULT 0,

    "total_fees" BLOB NOT NULL DEFAULT X'00',
    "burnt_fees" BLOB NOT NULL DEFAULT X'00',

    "orphaned" INTEGER NOT NULL DEFAULT 0,
    "fork_id" INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY ("hash")
);

CREATE INDEX IF NOT EXISTS "el_blocks_number_idx" ON "el_blocks" ("number" DESC);
CREATE INDEX IF NOT EXISTS "el_blocks_timestamp_idx" ON "el_blocks" ("timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_blocks_fee_recipient_idx" ON "el_blocks" ("fee_recipient");
CREATE INDEX IF NOT EXISTS "el_blocks_fork_id_idx" ON "el_blocks" ("fork_id");

-- Transactions
CREATE TABLE IF NOT EXISTS "el_transactions" (
    "hash" BLOB NOT NULL,
    "block_number" INTEGER NOT NULL,
    "block_hash" BLOB NOT NULL,
    "block_timestamp" INTEGER NOT NULL,
    "transaction_index" INTEGER NOT NULL,

    "from_address" BLOB NOT NULL,
    "to_address" BLOB,
    "value" BLOB NOT NULL,
    "nonce" INTEGER NOT NULL,

    "gas_limit" INTEGER NOT NULL,
    "gas_price" INTEGER,
    "max_fee_per_gas" INTEGER,
    "max_priority_fee_per_gas" INTEGER,
    "effective_gas_price" INTEGER,
    "gas_used" INTEGER NOT NULL,

    "input_data" BLOB,
    "method_id" BLOB,
    "transaction_type" INTEGER NOT NULL DEFAULT 0,

    "status" INTEGER NOT NULL DEFAULT 1,
    "error_message" TEXT,

    "contract_address" BLOB,
    "logs_count" INTEGER NOT NULL DEFAULT 0,
    "internal_tx_count" INTEGER NOT NULL DEFAULT 0,

    "orphaned" INTEGER NOT NULL DEFAULT 0,
    "fork_id" INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY ("hash", "fork_id")
);

CREATE INDEX IF NOT EXISTS "el_transactions_block_number_idx" ON "el_transactions" ("block_number" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_block_hash_idx" ON "el_transactions" ("block_hash");
CREATE INDEX IF NOT EXISTS "el_transactions_from_address_idx" ON "el_transactions" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_to_address_idx" ON "el_transactions" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_timestamp_idx" ON "el_transactions" ("block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_method_id_idx" ON "el_transactions" ("method_id");

-- Internal transactions (traces)
CREATE TABLE IF NOT EXISTS "el_internal_txs" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "transaction_hash" BLOB NOT NULL,
    "block_number" INTEGER NOT NULL,
    "block_timestamp" INTEGER NOT NULL,

    "trace_address" TEXT NOT NULL,
    "trace_type" TEXT NOT NULL,
    "call_type" TEXT,

    "from_address" BLOB NOT NULL,
    "to_address" BLOB,
    "value" BLOB NOT NULL DEFAULT X'00',
    "gas" INTEGER,
    "gas_used" INTEGER,
    "input_data" BLOB,
    "output_data" BLOB,

    "error" TEXT,
    "created_contract" BLOB
);

CREATE INDEX IF NOT EXISTS "el_internal_txs_tx_hash_idx" ON "el_internal_txs" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_internal_txs_from_address_idx" ON "el_internal_txs" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_txs_to_address_idx" ON "el_internal_txs" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_txs_timestamp_idx" ON "el_internal_txs" ("block_timestamp" DESC);

-- Events/Logs
CREATE TABLE IF NOT EXISTS "el_events" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "transaction_hash" BLOB NOT NULL,
    "block_number" INTEGER NOT NULL,
    "block_timestamp" INTEGER NOT NULL,
    "log_index" INTEGER NOT NULL,

    "address" BLOB NOT NULL,
    "topic0" BLOB,
    "topic1" BLOB,
    "topic2" BLOB,
    "topic3" BLOB,
    "data" BLOB,

    "event_name" TEXT,
    "decoded_data" TEXT
);

CREATE INDEX IF NOT EXISTS "el_events_tx_hash_idx" ON "el_events" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_events_address_idx" ON "el_events" ("address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_events_topic0_idx" ON "el_events" ("topic0");
CREATE INDEX IF NOT EXISTS "el_events_timestamp_idx" ON "el_events" ("block_timestamp" DESC);

-- Token contracts
CREATE TABLE IF NOT EXISTS "el_token_contracts" (
    "address" BLOB NOT NULL,
    "token_type" TEXT NOT NULL,

    "name" TEXT,
    "symbol" TEXT,
    "decimals" INTEGER,
    "total_supply" BLOB,

    "discovered_block" INTEGER NOT NULL,
    "discovered_timestamp" INTEGER NOT NULL,

    "holder_count" INTEGER NOT NULL DEFAULT 0,
    "transfer_count" INTEGER NOT NULL DEFAULT 0,
    "last_updated_block" INTEGER NOT NULL,

    PRIMARY KEY ("address")
);

CREATE INDEX IF NOT EXISTS "el_token_contracts_type_idx" ON "el_token_contracts" ("token_type");
CREATE INDEX IF NOT EXISTS "el_token_contracts_symbol_idx" ON "el_token_contracts" ("symbol");

-- Token balances
CREATE TABLE IF NOT EXISTS "el_token_balances" (
    "address" BLOB NOT NULL,
    "token_address" BLOB NOT NULL,
    "balance" BLOB NOT NULL,
    "updated_block" INTEGER NOT NULL,
    "updated_timestamp" INTEGER NOT NULL,

    PRIMARY KEY ("address", "token_address")
);

CREATE INDEX IF NOT EXISTS "el_token_balances_token_idx" ON "el_token_balances" ("token_address", "balance" DESC);
CREATE INDEX IF NOT EXISTS "el_token_balances_updated_idx" ON "el_token_balances" ("updated_timestamp");

-- Token transfers
CREATE TABLE IF NOT EXISTS "el_token_transfers" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT,
    "transaction_hash" BLOB NOT NULL,
    "block_number" INTEGER NOT NULL,
    "block_timestamp" INTEGER NOT NULL,
    "log_index" INTEGER NOT NULL,

    "token_address" BLOB NOT NULL,
    "token_type" TEXT NOT NULL,

    "from_address" BLOB NOT NULL,
    "to_address" BLOB NOT NULL,
    "value" BLOB,
    "token_id" BLOB
);

CREATE INDEX IF NOT EXISTS "el_token_transfers_tx_hash_idx" ON "el_token_transfers" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_token_transfers_token_idx" ON "el_token_transfers" ("token_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_idx" ON "el_token_transfers" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_to_idx" ON "el_token_transfers" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_timestamp_idx" ON "el_token_transfers" ("block_timestamp" DESC);

-- Method signatures
CREATE TABLE IF NOT EXISTS "el_method_signatures" (
    "signature" BLOB NOT NULL,
    "name" TEXT NOT NULL,
    "signature_text" TEXT NOT NULL,

    PRIMARY KEY ("signature")
);

-- Pre-populate with common methods
INSERT OR IGNORE INTO "el_method_signatures" ("signature", "name", "signature_text") VALUES
(X'a9059cbb', 'Transfer', 'transfer(address,uint256)'),
(X'23b872dd', 'Transfer From', 'transferFrom(address,address,uint256)'),
(X'095ea7b3', 'Approve', 'approve(address,uint256)'),
(X'38ed1739', 'Swap Exact Tokens For Tokens', 'swapExactTokensForTokens(uint256,uint256,address[],address,uint256)'),
(X'791ac947', 'Swap Exact Tokens For ETH', 'swapExactTokensForETH(uint256,uint256,address[],address,uint256)'),
(X'7ff36ab5', 'Swap Exact ETH For Tokens', 'swapExactETHForTokens(uint256,address[],address,uint256)'),
(X'18160ddd', 'Total Supply', 'totalSupply()'),
(X'70a08231', 'Balance Of', 'balanceOf(address)'),
(X'06fdde03', 'Name', 'name()'),
(X'95d89b41', 'Symbol', 'symbol()'),
(X'313ce567', 'Decimals', 'decimals()');

-- Event signatures
CREATE TABLE IF NOT EXISTS "el_event_signatures" (
    "signature" BLOB NOT NULL,
    "name" TEXT NOT NULL,
    "signature_text" TEXT NOT NULL,

    PRIMARY KEY ("signature")
);

-- Pre-populate with common events
INSERT OR IGNORE INTO "el_event_signatures" ("signature", "name", "signature_text") VALUES
(X'ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef', 'Transfer', 'Transfer(address,address,uint256)'),
(X'8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925', 'Approval', 'Approval(address,address,uint256)'),
(X'd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822', 'Swap', 'Swap(address,uint256,uint256,uint256,uint256,address)'),
(X'1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1', 'Sync', 'Sync(uint112,uint112)');

-- EL indexer state
CREATE TABLE IF NOT EXISTS "el_indexer_state" (
    "fork_id" INTEGER NOT NULL,
    "last_indexed_block" INTEGER NOT NULL,
    "last_indexed_timestamp" INTEGER NOT NULL,

    PRIMARY KEY ("fork_id")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS "el_indexer_state";
DROP TABLE IF EXISTS "el_event_signatures";
DROP TABLE IF EXISTS "el_method_signatures";
DROP TABLE IF EXISTS "el_token_transfers";
DROP TABLE IF EXISTS "el_token_balances";
DROP TABLE IF EXISTS "el_token_contracts";
DROP TABLE IF EXISTS "el_events";
DROP TABLE IF EXISTS "el_internal_txs";
DROP TABLE IF EXISTS "el_transactions";
DROP TABLE IF EXISTS "el_blocks";
DROP TABLE IF EXISTS "el_addresses";

-- +goose StatementEnd
