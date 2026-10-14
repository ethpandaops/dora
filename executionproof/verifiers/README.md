# Embedded zkVM verifiers

Dora verifies the EIP-8025 execution proofs it collects with the verifiers of
[ere](https://github.com/eth-act/ere), compiled to a wasm32 module and run in
process with wazero. One module holds the SP1, Zisk and OpenVM verifiers of one
ere release; the proofs of a devnet verify with the release its provers and
clients run (a Lighthouse `optional-proofs` build pins the same tag).

Proof systems bind their proofs to the verifier of the zkVM version that made
them (Zisk changes its proof format between releases, SP1 kept it across 6.x),
so the embedded releases cover the generations the devnets run: the one the
provers build with and the one the verifying clients pin.

A module is built from an ere tag in Docker, so no Rust toolchain is needed:

    ./build.sh v0.17.2

writes `wasm/ere-v0.17.2.wasm.sz` (snappy compressed) and the tag is registered in
`generations.go`, newest first. The verifiers try the registered generations
of a proof's zkVM in that order; a proof verifies under the generation it was
made with.

`shim/` is the crate that exports the verifiers with a C ABI (`ere_verify`),
`patches/` holds the one change the pinned OpenVM fork needs for wasm32 (its
executor asserts a 64-bit memory layout at compile time; the verifier links but
never runs it).

The builds are reproducible (pinned toolchain image, packages and dependency
lockfile); a rebuild of a tag yields the byte-identical module:

| ere release | module | SHA-256 |
| --- | --- | --- |
| v0.19.2 | `wasm/ere-v0.19.2.wasm.sz` | `cb3ad262f2e3b60b53d141f9187493d9c06c1664135192b23607e6045f7445e0` |
| v0.17.2 | `wasm/ere-v0.17.2.wasm.sz` | `05cde6f288f5666f47e74727772a272c78b534d74cf93895a0aed921e18477e4` |
