# TOGAF Architecture Documentation

> **Source**: [TOGAF/architecture_documentation.md](https://github.com/ethpar/dora.par/blob/main/TOGAF/architecture_documentation.md)
> 
> **Last Updated**: 2025-05-18

<!-- 
  This file should be manually updated by copying content from:
  /TOGAF/architecture_documentation.md
  
  To update, run:
  cp /TOGAF/architecture_documentation.md /docs/wiki/TOGAF-Documentation.md
  
  Then commit and push the changes.
-->

```markdown
# Dora Block Explorer - TOGAF Architecture Documentation

## 1. Architecture Vision

### 1.1 Deployment Architecture

Dora's architecture is designed to be highly available and fault-tolerant when interacting with Ethereum nodes. It supports connections to both Consensus Layer (CL) and Execution Layer (EL) clients, with built-in failover capabilities.

#### 1.1.1 Node Client Integration

Dora connects to Ethereum nodes using the following client types:

1. **Consensus Layer Clients**
   - Connects to Beacon Node clients (e.g., Lighthouse, Prysm, Teku, Nimbus, Lodestar)
   - Uses the standard Ethereum 2.0 Beacon Node API
   - Supports multiple endpoints with failover

2. **Execution Layer Clients**
   - Connects to Execution clients (e.g., Geth, Nethermind, Erigon, Besu)
   - Uses standard JSON-RPC over HTTP/HTTPS
   - Supports WebSocket subscriptions for real-time updates
   - Implements connection pooling for performance

#### 1.1.2 RPC Endpoints and API Groups

Dora interacts with the following RPC endpoint groups:

**Consensus Layer (Beacon Node) Endpoints:**
- `/eth/v1/beacon` - Beacon chain data
- `/eth/v1/node` - Node information and health
- `/eth/v1/config` - Network configuration
- `/eth/v1/debug` - Debug endpoints
- `/eth/v1/events` - Event subscriptions
- `/eth/v1/validator` - Validator operations (optional)

**Execution Layer Endpoints:**
- `eth_*` - Standard Ethereum JSON-RPC methods
- `net_*` - Network information
- `web3_*` - Web3 client information
- `debug_*` - Debug trace endpoints (if enabled)
```

## How to Update This Document

To update this documentation:

1. Edit the source file: `/TOGAF/architecture_documentation.md`
2. Copy the updated content to this file:
   ```bash
   cp /TOGAF/architecture_documentation.md /docs/wiki/TOGAF-Documentation.md
   ```
3. Update the "Last Updated" date at the top of this file
4. Commit and push your changes

## Viewing the Latest Version

For the most up-to-date version of this documentation, please visit the [source file](https://github.com/ethpar/dora.par/blob/main/TOGAF/architecture_documentation.md) directly.
