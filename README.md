# Dora the Beaconchain Explorer

[![Badge](https://github.com/ethpandaops/dora/actions/workflows/build-master.yml/badge.svg)](https://github.com/ethpandaops/dora/actions?query=workflow%3A%22Build+master%22)
[![Go Report Card](https://goreportcard.com/badge/github.com/ethpandaops/dora)](https://goreportcard.com/report/github.com/ethpandaops/dora)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ethpandaops/dora?label=Latest%20Release)](https://github.com/ethpandaops/dora/releases/latest)

## What is this?
This is a lightweight beaconchain explorer.

A Beaconchain explorer is a tool that allows users to view and interact with the data on the Ethereum Beacon Chain. It is similar to a blockchain explorer, which allows users to view data on a blockchain such as the current state of transactions and blocks - but focussed on exploring the beaconchain.

This "lightweight" explorer loads most of the information directly from an underlying standard beacon node api, which makes it a lot easier and cheaper to run (no 3rd party proprietary database like bigtables required).

## Testnet instances
[Holešky](https://github.com/eth-clients/holesky) Testnet: 
* https://dora-holesky.pk910.de/
* https://dora.holesky.ethpandaops.io/

[Sepolia](https://github.com/eth-clients/sepolia) Testnet: 
* https://dora.sepolia.ethpandaops.io/

[Ephemery](https://github.com/ephemery-testnet/ephemery-resources) Testnet: 
* https://beaconlight.ephemery.dev/

# Setup & Configuration
Read through the [wiki](https://github.com/ethpandaops/dora/wiki) for setup & configuration instructions.

## Dependencies

The explorer has no mandatory external dependencies. It can even run completely in memory only.\
However, for best performance I recommend using a PostgreSQL database.

## Background
https://github.com/ethpandaops/tooling-wishlist/blob/master/tools/lightweight-beaconchain-explorer.md

## Open Points / Ideas

Things that might be worth adding at some time

* [ ] Show Sync Committees
* [ ] Show Deposits

# Thanks To

This explorer is heavily based on the code from [gobitfly/eth2-beaconchain-explorer](https://github.com/gobitfly/eth2-beaconchain-explorer).

# License

[![License: GPL-3.0](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
