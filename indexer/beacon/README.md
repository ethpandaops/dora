# Dora: beaconchain indexer

Welcome to the documentation for the `indexer` package of Dora the Beacon Chain Explorer. 
This package is responsible for indexing the beacon chain, including blocks, duties, and the current chain state. 
It consumes events from one or more `pool.consensus.Client` clients, updates database indexes, and provides functions 
to gain insights into the current network state.

## Overview

The indexer package is divided into several components:
- Initialization routine
- Input processor
- Caching subsystems
  - Block cache
  - Epoch cache
  - Fork cache
- Processing routines
  - Finalization
  - Pruning
  - Synchronization

## Initialization Routine

The initialization routine prefills all caching subsystems with the latest preserved state from the database. It uses the currently finalized epoch as a boundary for loading entities, so only objects between the current wallclock epoch and the current finalized epoch are restored. Entities older than `inMemoryEpochs` are directly pruned to save memory. This process runs before the indexer starts to receive events from the attached clients. For networks with large unfinality periods, this might take a few seconds to minutes.

## Input Processor

The input processor consumes events from an underlying consensus pool client and feeds the caching subsystems with new data. It:
- Attaches to "block" and "head" events.
- Processes each received block, loading its block header and body.
- Ensures the completeness of the block cache by backfilling parent blocks.
- Dumps fully received blocks into the unfinalized blocks table for restoration after restart.
- Tracks the latest head of the client and detects chain reorgs.

An instance of the input processor is created for every client attached to the indexer via `indexer.AddClient`.

## Caching Subsystems

### Block Cache

The block cache subsystem keeps an overview of all unfinalized blocks in the network. It:
- Provides block accessors by various block properties.
- Checks block relationships and calculates distances between blocks.
- Might hold pruned blocks (without block bodies), but bodies can be restored on purpose.

### Epoch Cache

The epoch cache subsystem maintains an overview of unfinalized epoch stats. It:
- Holds duty and epoch statistics for each epoch.
- Identifies epoch stats by the epoch's dependent root and number.
- Might hold pruned epoch stats, keeping only proposer + sync committee duties and aggregations in memory
- Can restores full epoch stats from the database, which is an expensive operation.
- Can pre-calculate the next epoch stats, however this might not be accurate all the time.

### Fork Cache

The fork cache subsystem keeps an overview of all unfinalized forks in the network. It:
- Identifies forks by the base block root and leaf block root.
- Numbers forks with an ascending internal counter.
- Holds the fork detection system to identify current fork IDs and detect possible forks.
- Uses `forkId` as a placeholder for unknown or canonical finalized forks.

## Processing Routines

### Finalization Routine

The finalization routine processes unfinalized blocks from the block cache after finalization. It:
- Writes blocks, epoch aggregations, and child objects to the database.
- Stores orphaned blocks in the orphaned blocks table.
- Wipes processed blocks and epoch stats from caches.

### Pruning Routine

The pruning routine pre-finalizes old unfinalized epochs and prunes memory-heavy data from cache. It:
- Is controlled by the `inMemoryEpochs` setting.
- Generates non-final epoch voting aggregations.
- Writes blocks and child objects to the database.
- Stores pruned epoch aggregations in cache and the unfinalized epochs table for restoration.

### Synchronization Routine

The synchronization routine processes all missed historic data. It:
- Ensures each epoch is properly indexed.
- Loads canonical blocks and dependent states from a ready node.
- Computes epoch aggregations and writes them, along with canonical blocks and child objects, to the database.
- Is triggered by failed finalization or the initialization routine.
