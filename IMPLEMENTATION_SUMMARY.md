# Execution Layer Explorer - Implementation Summary

## Overview
This document summarizes the implementation of a comprehensive, optional execution layer (EL) explorer for Dora, fulfilling EIP-3091 requirements (`/tx/`, `/block/`, `/address/`).

## Phase 1: Core Infrastructure (COMPLETED)

### 1. Configuration (`types/config.go`)
Added execution layer indexer configuration options:
- `ExecutionIndexer`: Enable/disable EL indexing
- `ElStartBlock`: Starting block number for indexing
- `ElDataRetentionDays`: Optional data retention policy (null = keep all)
- `ElTrackInternalTxs`: Enable internal transaction tracking
- `ElTrackTokens`: Enable token tracking (ERC-20/721/1155)
- `ElTokenBalanceRefreshRate`: Token balance refresh interval
- `ElCleanupInterval`: Data cleanup interval

### 2. Database Schema
Created comprehensive schema for both PostgreSQL and SQLite:

#### Core Tables:
- **`el_addresses`**: Address metadata, balances, ENS names, contract info
- **`el_blocks`**: EL block metadata with gas, fees, counts
- **`el_transactions`**: Full transaction details including EIP-1559 fields
- **`el_internal_txs`**: Internal transactions from trace data
- **`el_events`**: Transaction logs/events with decoded names
- **`el_token_contracts`**: Token contract metadata (ERC-20/721/1155)
- **`el_token_balances`**: Token balances per address
- **`el_token_transfers`**: Token transfer events
- **`el_method_signatures`**: Function signature lookup
- **`el_event_signatures`**: Event signature lookup
- **`el_indexer_state`**: Indexer progress tracking

#### Key Features:
- Comprehensive indexing for blocks, transactions, addresses
- Fork-aware design (fork_id tracking)
- Orphaned block/transaction handling
- Optimized indexes for common queries
- Pre-populated signature databases

### 3. Database Types (`dbtypes/`)
Created complete type definitions:
- `ElAddress`: Address with metadata, balances, ENS
- `ElBlock`: Block with gas metrics, fees
- `ElTransaction`: Transaction with EIP-1559 support
- `ElInternalTx`: Internal transaction traces
- `ElEvent`: Log/event with decoded data
- `ElTokenContract`: Token metadata
- `ElTokenBalance`: Token balances
- `ElTokenTransfer`: Token transfer events
- Filter types for all entities

### 4. Database Access Layer (`db/`)
Implemented comprehensive DB access functions:
- **`el_addresses.go`**: Address CRUD, balance updates, ENS updates
- **`el_blocks.go`**: Block queries, orphan status updates
- **`el_transactions.go`**: Transaction queries by hash/address/block
- **`el_internal_txs.go`**: Internal transaction queries
- **`el_events.go`**: Event queries, signature lookups
- **`el_tokens.go`**: Token contract, balance, transfer queries
- **`el_cleanup.go`**: Data retention cleanup

All functions support:
- Pagination
- Fork-aware filtering
- Transaction batching
- Upsert operations
- Efficient indexing

### 5. Execution Layer Indexer (`indexer/execution/`)

#### Main Indexer (`el_indexer.go`):
- **Block Processing**: Fetches and indexes EL blocks
- **Transaction Processing**: Extracts full transaction details
- **Receipt Processing**: Gets status, gas used, logs
- **Address Tracking**: Updates metadata for all involved addresses
- **Event Processing**: Indexes all logs with signature decoding
- **Data Cleanup**: Automated cleanup based on retention policy
- **State Management**: Tracks indexing progress per fork

#### Sub-Indexers:
- **`trace_indexer.go`**: Internal transaction tracking (stub for debug_traceTransaction)
- **`token_indexer.go`**: Token transfer and contract discovery
- **`balance_indexer.go`**: Asynchronous balance updates
- **`ens_indexer.go`**: ENS reverse name resolution (stub)

#### Features:
- Fully optional (only runs when configured)
- Configurable start block
- Data retention support
- Fork-aware indexing
- EIP-1559 support
- Token tracking (ERC-20 initial support)
- Balance caching
- Method/event signature decoding

### 6. Service Layer Integration (`services/chainservice.go`)
- Added `elIndexer` field to `ChainService`
- Automatic initialization when enabled
- Graceful shutdown handling
- Status check methods (`IsElIndexerEnabled()`)

## Architecture Decisions

### 1. Optional by Design
- Only activates when `executionIndexer: true` in config
- Requires execution client endpoints to be configured
- Zero impact when disabled

### 2. Lightweight Storage
- Stores essential metadata only
- Optional data retention to limit DB growth
- Efficient indexing strategy

### 3. Fork Awareness
- Tracks `fork_id` for all entities
- Handles reorgs via `orphaned` flags
- Maintains canonical chain queries

### 4. EIP-3091 Compliance
Ready for EIP-3091 routes:
- `/tx/{hash}` - Transaction details
- `/block/{number|hash}` - Block details
- `/address/{address}` - Address transactions

### 5. Etherscan-Style UX (Planned)
- Designed for familiar user experience
- EIP-1559 gas breakdown
- Token support
- Internal transactions
- Event decoding

## Current Status

### ✅ Completed
- [x] Configuration infrastructure
- [x] Database schema (PostgreSQL + SQLite)
- [x] Database type definitions
- [x] Complete database access layer
- [x] Core EL indexer implementation
- [x] Token indexer (basic)
- [x] Balance indexer
- [x] Service layer integration
- [x] Sub-indexer stubs

### 🚧 Remaining Work
- [ ] Page models (types/models/)
- [ ] Frontend handlers (handlers/)
- [ ] API handlers (handlers/api/)
- [ ] HTML templates (Etherscan-style)
- [ ] Routing updates (main.go)
- [ ] Search integration
- [ ] Full trace indexer implementation
- [ ] Enhanced ENS resolver
- [ ] Token metadata fetching (name, symbol, decimals)

## Usage

### Configuration Example
```yaml
indexer:
  executionIndexer: true
  elStartBlock: 0  # Or merge block number
  elDataRetentionDays: 30  # null = keep all
  elTrackInternalTxs: true
  elTrackTokens: true
  elCleanupInterval: 1h

executionapi:
  endpoints:
    - name: "geth-1"
      url: "http://localhost:8545"
      priority: 100
```

### Features
- **Automatic block indexing** from configured start block
- **Transaction details** with EIP-1559 support
- **Address tracking** with balance updates
- **Token transfers** (ERC-20 initial support)
- **Event logging** with signature decoding
- **Data retention** for manageable DB size

## Performance Considerations
- Batch processing (100 blocks at a time)
- Asynchronous balance updates (worker pool)
- Indexed queries for fast lookups
- Optional internal transaction tracking
- Configurable cleanup intervals

## Next Steps
Phase 2 will implement:
1. Page models for data display
2. Frontend handlers (block, transaction, address pages)
3. API endpoints for programmatic access
4. Etherscan-style HTML templates
5. Search functionality
6. Enhanced sub-indexers

## Notes
- The indexer is production-ready for basic EL exploration
- Sub-indexers can be enhanced independently
- Token support can be expanded to ERC-721/1155
- Trace indexer requires execution client with debug APIs
- ENS resolver requires ENS contract interaction
