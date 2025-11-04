-- +goose Up
-- +goose StatementBegin

-- Execution Layer Explorer Tables

-- Address metadata and stats
CREATE TABLE IF NOT EXISTS public."el_addresses" (
    "address" bytea NOT NULL,
    "is_contract" boolean NOT NULL DEFAULT false,
    "contract_creator" bytea,
    "creation_tx_hash" bytea,
    "creation_block_number" bigint,
    "creation_timestamp" bigint,

    -- Transaction stats
    "first_seen_block" bigint NOT NULL,
    "first_seen_timestamp" bigint NOT NULL,
    "last_seen_block" bigint NOT NULL,
    "last_seen_timestamp" bigint NOT NULL,
    "tx_count" bigint NOT NULL DEFAULT 0,
    "in_tx_count" bigint NOT NULL DEFAULT 0,
    "out_tx_count" bigint NOT NULL DEFAULT 0,

    -- Balance tracking
    "balance" bytea NOT NULL DEFAULT E'\\x00',
    "balance_updated_block" bigint NOT NULL DEFAULT 0,

    -- Naming
    "ens_name" text,
    "ens_updated_at" bigint,
    "custom_name" text,

    -- Contract metadata
    "contract_bytecode_hash" bytea,
    "contract_verified" boolean NOT NULL DEFAULT false,

    CONSTRAINT "el_addresses_pkey" PRIMARY KEY ("address")
);

CREATE INDEX IF NOT EXISTS "el_addresses_is_contract_idx"
    ON public."el_addresses" ("is_contract");
CREATE INDEX IF NOT EXISTS "el_addresses_tx_count_idx"
    ON public."el_addresses" ("tx_count" DESC);
CREATE INDEX IF NOT EXISTS "el_addresses_balance_idx"
    ON public."el_addresses" ("balance" DESC) WHERE "balance" != E'\\x00';
CREATE INDEX IF NOT EXISTS "el_addresses_ens_idx"
    ON public."el_addresses" ("ens_name") WHERE "ens_name" IS NOT NULL;

-- Execution layer blocks
CREATE TABLE IF NOT EXISTS public."el_blocks" (
    "number" bigint NOT NULL,
    "hash" bytea NOT NULL,
    "parent_hash" bytea NOT NULL,
    "timestamp" bigint NOT NULL,
    "fee_recipient" bytea NOT NULL,
    "state_root" bytea NOT NULL,
    "receipts_root" bytea NOT NULL,
    "logs_bloom" bytea NOT NULL,

    "difficulty" bytea,
    "total_difficulty" bytea,
    "size" bigint NOT NULL,
    "gas_used" bigint NOT NULL,
    "gas_limit" bigint NOT NULL,
    "base_fee_per_gas" bigint,
    "extra_data" bytea,
    "nonce" bytea,

    "transaction_count" integer NOT NULL DEFAULT 0,
    "internal_tx_count" integer NOT NULL DEFAULT 0,
    "event_count" integer NOT NULL DEFAULT 0,
    "withdrawal_count" integer NOT NULL DEFAULT 0,

    "total_fees" bytea NOT NULL DEFAULT E'\\x00',
    "burnt_fees" bytea NOT NULL DEFAULT E'\\x00',

    "orphaned" boolean NOT NULL DEFAULT false,
    "fork_id" bigint NOT NULL DEFAULT 0,

    CONSTRAINT "el_blocks_pkey" PRIMARY KEY ("hash")
);

CREATE INDEX IF NOT EXISTS "el_blocks_number_idx"
    ON public."el_blocks" ("number" DESC);
CREATE INDEX IF NOT EXISTS "el_blocks_timestamp_idx"
    ON public."el_blocks" ("timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_blocks_fee_recipient_idx"
    ON public."el_blocks" ("fee_recipient");
CREATE INDEX IF NOT EXISTS "el_blocks_fork_id_idx"
    ON public."el_blocks" ("fork_id");

-- Transactions
CREATE TABLE IF NOT EXISTS public."el_transactions" (
    "hash" bytea NOT NULL,
    "block_number" bigint NOT NULL,
    "block_hash" bytea NOT NULL,
    "block_timestamp" bigint NOT NULL,
    "transaction_index" integer NOT NULL,

    "from_address" bytea NOT NULL,
    "to_address" bytea,
    "value" bytea NOT NULL,
    "nonce" bigint NOT NULL,

    "gas_limit" bigint NOT NULL,
    "gas_price" bigint,
    "max_fee_per_gas" bigint,
    "max_priority_fee_per_gas" bigint,
    "effective_gas_price" bigint,
    "gas_used" bigint NOT NULL,

    "input_data" bytea,
    "method_id" bytea,
    "transaction_type" smallint NOT NULL DEFAULT 0,

    "status" smallint NOT NULL DEFAULT 1,
    "error_message" text,

    "contract_address" bytea,
    "logs_count" integer NOT NULL DEFAULT 0,
    "internal_tx_count" integer NOT NULL DEFAULT 0,

    "orphaned" boolean NOT NULL DEFAULT false,
    "fork_id" bigint NOT NULL DEFAULT 0,

    CONSTRAINT "el_transactions_pkey" PRIMARY KEY ("hash", "fork_id")
);

CREATE INDEX IF NOT EXISTS "el_transactions_block_number_idx"
    ON public."el_transactions" ("block_number" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_block_hash_idx"
    ON public."el_transactions" ("block_hash");
CREATE INDEX IF NOT EXISTS "el_transactions_from_address_idx"
    ON public."el_transactions" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_to_address_idx"
    ON public."el_transactions" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_timestamp_idx"
    ON public."el_transactions" ("block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_transactions_method_id_idx"
    ON public."el_transactions" ("method_id");

-- Internal transactions (traces)
CREATE TABLE IF NOT EXISTS public."el_internal_txs" (
    "id" bigserial NOT NULL,
    "transaction_hash" bytea NOT NULL,
    "block_number" bigint NOT NULL,
    "block_timestamp" bigint NOT NULL,

    "trace_address" text NOT NULL,
    "trace_type" text NOT NULL,
    "call_type" text,

    "from_address" bytea NOT NULL,
    "to_address" bytea,
    "value" bytea NOT NULL DEFAULT E'\\x00',
    "gas" bigint,
    "gas_used" bigint,
    "input_data" bytea,
    "output_data" bytea,

    "error" text,
    "created_contract" bytea,

    CONSTRAINT "el_internal_txs_pkey" PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "el_internal_txs_tx_hash_idx"
    ON public."el_internal_txs" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_internal_txs_from_address_idx"
    ON public."el_internal_txs" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_txs_to_address_idx"
    ON public."el_internal_txs" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_internal_txs_timestamp_idx"
    ON public."el_internal_txs" ("block_timestamp" DESC);

-- Events/Logs
CREATE TABLE IF NOT EXISTS public."el_events" (
    "id" bigserial NOT NULL,
    "transaction_hash" bytea NOT NULL,
    "block_number" bigint NOT NULL,
    "block_timestamp" bigint NOT NULL,
    "log_index" integer NOT NULL,

    "address" bytea NOT NULL,
    "topic0" bytea,
    "topic1" bytea,
    "topic2" bytea,
    "topic3" bytea,
    "data" bytea,

    "event_name" text,
    "decoded_data" jsonb,

    CONSTRAINT "el_events_pkey" PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "el_events_tx_hash_idx"
    ON public."el_events" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_events_address_idx"
    ON public."el_events" ("address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_events_topic0_idx"
    ON public."el_events" ("topic0");
CREATE INDEX IF NOT EXISTS "el_events_timestamp_idx"
    ON public."el_events" ("block_timestamp" DESC);

-- Token contracts
CREATE TABLE IF NOT EXISTS public."el_token_contracts" (
    "address" bytea NOT NULL,
    "token_type" text NOT NULL,

    "name" text,
    "symbol" text,
    "decimals" smallint,
    "total_supply" bytea,

    "discovered_block" bigint NOT NULL,
    "discovered_timestamp" bigint NOT NULL,

    "holder_count" bigint NOT NULL DEFAULT 0,
    "transfer_count" bigint NOT NULL DEFAULT 0,
    "last_updated_block" bigint NOT NULL,

    CONSTRAINT "el_token_contracts_pkey" PRIMARY KEY ("address")
);

CREATE INDEX IF NOT EXISTS "el_token_contracts_type_idx"
    ON public."el_token_contracts" ("token_type");
CREATE INDEX IF NOT EXISTS "el_token_contracts_symbol_idx"
    ON public."el_token_contracts" ("symbol");

-- Token balances
CREATE TABLE IF NOT EXISTS public."el_token_balances" (
    "address" bytea NOT NULL,
    "token_address" bytea NOT NULL,
    "balance" bytea NOT NULL,
    "updated_block" bigint NOT NULL,
    "updated_timestamp" bigint NOT NULL,

    CONSTRAINT "el_token_balances_pkey" PRIMARY KEY ("address", "token_address")
);

CREATE INDEX IF NOT EXISTS "el_token_balances_token_idx"
    ON public."el_token_balances" ("token_address", "balance" DESC);
CREATE INDEX IF NOT EXISTS "el_token_balances_updated_idx"
    ON public."el_token_balances" ("updated_timestamp");

-- Token transfers
CREATE TABLE IF NOT EXISTS public."el_token_transfers" (
    "id" bigserial NOT NULL,
    "transaction_hash" bytea NOT NULL,
    "block_number" bigint NOT NULL,
    "block_timestamp" bigint NOT NULL,
    "log_index" integer NOT NULL,

    "token_address" bytea NOT NULL,
    "token_type" text NOT NULL,

    "from_address" bytea NOT NULL,
    "to_address" bytea NOT NULL,
    "value" bytea,
    "token_id" bytea,

    CONSTRAINT "el_token_transfers_pkey" PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "el_token_transfers_tx_hash_idx"
    ON public."el_token_transfers" ("transaction_hash");
CREATE INDEX IF NOT EXISTS "el_token_transfers_token_idx"
    ON public."el_token_transfers" ("token_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_from_idx"
    ON public."el_token_transfers" ("from_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_to_idx"
    ON public."el_token_transfers" ("to_address", "block_timestamp" DESC);
CREATE INDEX IF NOT EXISTS "el_token_transfers_timestamp_idx"
    ON public."el_token_transfers" ("block_timestamp" DESC);

-- Method signatures
CREATE TABLE IF NOT EXISTS public."el_method_signatures" (
    "signature" bytea NOT NULL,
    "name" text NOT NULL,
    "signature_text" text NOT NULL,

    CONSTRAINT "el_method_signatures_pkey" PRIMARY KEY ("signature")
);

-- Pre-populate with common methods
INSERT INTO "el_method_signatures" ("signature", "name", "signature_text") VALUES
(E'\\xa9059cbb', 'Transfer', 'transfer(address,uint256)'),
(E'\\x23b872dd', 'Transfer From', 'transferFrom(address,address,uint256)'),
(E'\\x095ea7b3', 'Approve', 'approve(address,uint256)'),
(E'\\x38ed1739', 'Swap Exact Tokens For Tokens', 'swapExactTokensForTokens(uint256,uint256,address[],address,uint256)'),
(E'\\x791ac947', 'Swap Exact Tokens For ETH', 'swapExactTokensForETH(uint256,uint256,address[],address,uint256)'),
(E'\\x7ff36ab5', 'Swap Exact ETH For Tokens', 'swapExactETHForTokens(uint256,address[],address,uint256)'),
(E'\\x18160ddd', 'Total Supply', 'totalSupply()'),
(E'\\x70a08231', 'Balance Of', 'balanceOf(address)'),
(E'\\x06fdde03', 'Name', 'name()'),
(E'\\x95d89b41', 'Symbol', 'symbol()'),
(E'\\x313ce567', 'Decimals', 'decimals()')
ON CONFLICT DO NOTHING;

-- Event signatures
CREATE TABLE IF NOT EXISTS public."el_event_signatures" (
    "signature" bytea NOT NULL,
    "name" text NOT NULL,
    "signature_text" text NOT NULL,

    CONSTRAINT "el_event_signatures_pkey" PRIMARY KEY ("signature")
);

-- Pre-populate with common events
INSERT INTO "el_event_signatures" ("signature", "name", "signature_text") VALUES
(E'\\xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef', 'Transfer', 'Transfer(address,address,uint256)'),
(E'\\x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925', 'Approval', 'Approval(address,address,uint256)'),
(E'\\xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822', 'Swap', 'Swap(address,uint256,uint256,uint256,uint256,address)'),
(E'\\x1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1', 'Sync', 'Sync(uint112,uint112)')
ON CONFLICT DO NOTHING;

-- EL indexer state
CREATE TABLE IF NOT EXISTS public."el_indexer_state" (
    "fork_id" bigint NOT NULL,
    "last_indexed_block" bigint NOT NULL,
    "last_indexed_timestamp" bigint NOT NULL,

    CONSTRAINT "el_indexer_state_pkey" PRIMARY KEY ("fork_id")
);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public."el_indexer_state";
DROP TABLE IF EXISTS public."el_event_signatures";
DROP TABLE IF EXISTS public."el_method_signatures";
DROP TABLE IF EXISTS public."el_token_transfers";
DROP TABLE IF EXISTS public."el_token_balances";
DROP TABLE IF EXISTS public."el_token_contracts";
DROP TABLE IF EXISTS public."el_events";
DROP TABLE IF EXISTS public."el_internal_txs";
DROP TABLE IF EXISTS public."el_transactions";
DROP TABLE IF EXISTS public."el_blocks";
DROP TABLE IF EXISTS public."el_addresses";

-- +goose StatementEnd
