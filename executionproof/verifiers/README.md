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
never runs it). The module is reduced to what the shim's exports reach: the
release profile (fat LTO, one codegen unit, `panic = "abort"`) is applied at
the ere workspace root, `wasm-metadce` (`metadce.json` names the roots) drops
the wasm-bindgen glue that Zisk's dependencies export, `wasm-opt -Oz` shrinks
the rest and `pack` strips the custom sections before compressing.

The builds are reproducible (pinned toolchain image, packages and dependency
lockfile); a rebuild of a tag yields the byte-identical module:

| ere release | module | SHA-256 |
| --- | --- | --- |
| v0.19.2 | `wasm/ere-v0.19.2.wasm.sz` | `35ef1470d7901e9c86a340c974de4b6264521ece44ce4b3e0a45c612e59e802b` |
| v0.17.2 | `wasm/ere-v0.17.2.wasm.sz` | `c3e00aee5827af3ea0ab09c83484574c4a4d4bbd52fdfe41bc9b37418d9d1091` |
