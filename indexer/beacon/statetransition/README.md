# statetransition

[![spec vectors v1.7.0-beta.4](https://img.shields.io/github/actions/workflow/status/ethpandaops/dora/test-spec-vectors.yml?branch=master&event=push&label=spec%20vectors%20v1.7.0-beta.4)](https://github.com/ethpandaops/dora/actions/workflows/test-spec-vectors.yml?query=branch%3Amaster+event%3Apush)
[![spec vectors, newest release](https://img.shields.io/github/actions/workflow/status/ethpandaops/dora/test-spec-vectors.yml?branch=master&event=schedule&label=spec%20vectors%2C%20newest%20release)](https://github.com/ethpandaops/dora/actions/workflows/test-spec-vectors.yml?query=branch%3Amaster+event%3Aschedule)
[![newest spec release](https://img.shields.io/github/v/release/ethereum/consensus-specs?include_prereleases&label=newest%20spec%20release)](https://github.com/ethereum/consensus-specs/releases)

The beacon chain state transition for Fulu, Gloas and Heze states, as the
[consensus specs](https://github.com/ethereum/consensus-specs) define it:
`process_slots`, `process_epoch`, `process_block` and the `upgrade_to_*`
fork upgrades, applied in place to a `all.BeaconState`.

Dora uses it to replay blocks onto a state it holds and to derive the
pre-epoch states the beacon API cannot serve. Pre-Fulu states are out of scope:
the values Dora needs from them are read from the states the node serves.

## Spec version

The package implements consensus-specs **v1.7.0-beta.4**. The release is
recorded in `SpecVersion` (statetransition.go), which the spec vector workflow
and `.github/scripts/fetch-spec-tests.sh pinned` read.

The badges above show, for `master`:

1. the pinned release's vectors, replayed on every change to the package,
2. the newest release's vectors, replayed on a schedule - red means the specs
   moved ahead of the package,
3. the newest consensus-specs release itself.

## What is left out

Validity checks that a beacon node has already enforced on the blocks Dora
replays are skipped: every spec `assert` (signatures, Merkle proofs, slot and
index bounds, ...). The vectors that expect a rejected block or operation are
therefore skipped, not run.

Pre-Fulu forks are not implemented, so neither are their upgrades: the only
fork upgrades are `upgrade_to_gloas` and `upgrade_to_heze`.

## Spec vectors

`spectests_test.go` replays the reference vectors of a release through the
package: `sanity`, `finality`, `random`, `operations`, `epoch_processing`,
`fork` and `transition`. The `rewards` suite is skipped - it asserts
per-validator delta arrays while the rewards are applied inline here - as are
the suites that are not state transition tests.

```sh
.github/scripts/fetch-spec-tests.sh                 # pinned release, minimal preset
.github/scripts/fetch-spec-tests.sh latest mainnet  # newest release, mainnet preset
.github/scripts/fetch-spec-tests.sh v1.7.0-beta.4 minimal mainnet

SPEC_TESTS_DIR=$PWD/.spec-tests/v1.7.0-beta.4 \
    go test ./indexer/beacon/statetransition/ -run TestSpecVectors -v
```

`SPEC_TESTS_PRESET`, `SPEC_TESTS_FORK`, `SPEC_TESTS_RUNNER` and
`SPEC_TESTS_CASE` narrow the run; `SPEC_TESTS_DIR` unset skips it, so a plain
`go test ./...` stays fast. A failing case names the first state field that
differs from the expected post state.

## Catching up with a release

1. Diff the spec functions between the pinned and the new release. The raw
   markdown diff is dominated by prose and pyspec style changes; diffing the
   ```` ```python ```` blocks of `specs/*/*.md` split into their top-level
   `def`/`class` chunks leaves the functional changes. Also check `presets/`,
   `configs/` and `ssz/`.
2. Apply the changes, mirroring the spec function names in the Go function
   names and doc comments.
3. Run the new release's vectors (`fetch-spec-tests.sh <tag>`), minimal and
   mainnet, until they pass.
4. Bump `SpecVersion` and the version in this README (prose and first badge).
