# Lightweight Beaconchain Explorer

<b>This is a work in progress project!\
It's not ready to be used in any way yet.</b>

## What is this?
This project is planned to become a lightweight beaconchain explorer.

A Beaconchain explorer is a tool that allows users to view and interact with the data on the Ethereum Beacon Chain. It is similar to a blockchain explorer, which allows users to view data on a blockchain such as the current state of transactions and blocks - but focussed on exploring the beaconchain.

This "lightweight" explorer is planned to proxy most of the queries to an underlying standard beacon node api, which makes it a lot easier and cheaper to run (no 3rd party proprietary database like bigtables required)

## Background
https://github.com/ethpandaops/tooling-wishlist/blob/master/tools/lightweight-beaconchain-explorer.md

## TODO

First Version TODO:

* Explorer Pages (UI)
  * [ ] Layout polishing
  * [x] Startpage
    * [ ] Add Network Status (number of validators / deposit & exit queue / ...?)
  * [x] Epoch Overview (`/epochs`)
  * [x] Epoch details (`/epoch/{epoch}`)
  * [x] Slots Overview (`/slots`)
  * [x] Slot details (`/slot/{slot_or_root}`)
    * [x] Overview, Attestations, Slashings, Deposits, BLSChanges, Withdrawals, Voluntary Exits
    * [x] Blob Sidecars
    * [x] Enhance view controls (Hex/UTF8 Grafitti, Local/UTC Time, Copy Buttons etc.)
    * [ ] Load orphaned blocks from db
  * [x] Search (Block Root, Epoch Number, Slot Number, Grafitti)
    * [x] Type-Ahead search
* RPC Client / Caching
  * [x] Get Block Header by slot / block root
  * [x] Get Block Body by block root
  * [x] Get Epoch assignments (proposer, attester & sync committee duties)
    * [x] Simple cache for epoch duties
* Database
  * [ ] Schema initialization / upgrade
  * [x] Table: blocks (Block index for search & slot overview)
  * [x] Table: epochs (Epoch index for startpage & epoch overview)
  * [x] Table: explorer_state (simple key-value table to track of various states)
  * [x] Table: slot_assignments (Slot duties for search and block lists)
* Indexer
  * [x] Keep track of current & last epoch in memory
  * [x] Aggregate Votes
  * [x] Check for chain reorgs and track orphaned blocks
    * [x] Save orphaned blocks in db (header & body)
    * [ ] Handle large chain reorgs with >32 slots (needs re-indexing of affected epochs)
  * [x] Update Slot index in DB
  * [x] Update Epoch index in DB
  * [x] Synchronization (index older epochs)

Advanced TODO (Things that might be worth adding after the first version is ready)

* Validator Overview & Details
  * [ ] Page: Validators List (`/validators`)
  * [ ] Page: Validator Details (`/validator/{validator_index}`)
    * [ ] Rough overview with status (activated, slashed, ...) & current balance
    * [ ] Recent Blocks (from db) 
    * [ ] Recent Attestations (from cache) 
* Track Sync Committees
  * [ ] Database: table sync_committees (Sync Committee index)
  * [ ] Indexer: track sync committees & aggregate participation per validator
  * [ ] UI: show recent sync committee assignments on Validator Details page
* Track Deposits
  * [ ] Database: table deposits (Deposit index)
  * [ ] RPC Client: get deposit contract events from EL client
  * [ ] Indexer: track deposits
  * [ ] UI: deposits list


# Thanks To

This explorer is heavily based on the code from [gobitfly/eth2-beaconchain-explorer](https://github.com/gobitfly/eth2-beaconchain-explorer).

# License

[![License: GPL-3.0](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
