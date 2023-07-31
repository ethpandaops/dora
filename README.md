# Lightweight Beaconchain Explorer

## What is this?
This is a lightweight beaconchain explorer.

A Beaconchain explorer is a tool that allows users to view and interact with the data on the Ethereum Beacon Chain. It is similar to a blockchain explorer, which allows users to view data on a blockchain such as the current state of transactions and blocks - but focussed on exploring the beaconchain.

This "lightweight" explorer loads most of the information directly from an underlying standard beacon node api, which makes it a lot easier and cheaper to run (no 3rd party proprietary database like bigtables required).

# Setup & Configuration
Read through the [wiki](https://github.com/pk910/light-beaconchain-explorer/wiki) for setup & configuration instructions.

## Dependencies

This explorer requires a postgres database.

## Background
https://github.com/ethpandaops/tooling-wishlist/blob/master/tools/lightweight-beaconchain-explorer.md

## Open Points / Ideas

Things that might be worth adding at some time

* Validator Overview & Details\
  The current validator set is actually already maintained in memory. So it should be easy to add pages for basic validator related stuff.
  * [x] Page: Validators List (`/validators`)
  * [x] Page: Validator Details (`/validator/{validator_index}`)
    * [x] Rough overview with status (activated, slashed, ...) & current balance
    * [x] Recent Blocks (from db) 
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
