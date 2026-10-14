# Builds the ere zkVM verifiers (SP1, Zisk, OpenVM) as one wasm32 module from an
# ere release tag. Run through build.sh; nothing here needs a local Rust setup.
#
# The build is reproducible: the toolchain image is pinned by digest, the
# packages by version, the ere sources by tag and the Rust dependencies by
# ere's lockfile (the shim pins its own to versions in that lockfile), so a
# rebuild of a tag yields the byte-identical module.
FROM rust:1.98-bookworm@sha256:93ce27a88655056a51dbdd8f5f2d7ddc071c7b0070fb288a37b5a285fc83971e AS build

ARG ERE_TAG
RUN test -n "$ERE_TAG"

RUN apt-get update && apt-get install -y --no-install-recommends \
        clang=1:14.0-55.7~deb12u1 binaryen=108-1 patch=2.7.6-7 git=1:2.39.5-0+deb12u3 ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && rustup target add wasm32-unknown-unknown

WORKDIR /src
RUN git clone --depth 1 --branch "$ERE_TAG" https://github.com/eth-act/ere.git ere

# the shim crate joins the ere workspace next to the verifier crates
COPY shim /src/ere/crates/verifier/wasm
RUN sed -i 's|    "crates/verifier/zisk",|    "crates/verifier/zisk",\n    "crates/verifier/wasm",|' ere/Cargo.toml \
    && grep -q 'crates/verifier/wasm' ere/Cargo.toml

# fetch the sources, then patch the pinned OpenVM checkout for wasm32
WORKDIR /src/ere
RUN cargo fetch --target wasm32-unknown-unknown
COPY patches /src/patches
RUN files="$(find /usr/local/cargo/git/checkouts -path '*/crates/rvr/rvr-state/src/checkpoint_preflight.rs')" \
    && test -n "$files" \
    && for file in $files; do \
        patch -p1 --forward -d "${file%/crates/rvr/rvr-state/src/checkpoint_preflight.rs}" < /src/patches/openvm-wasm32.patch; \
    done

# zstd (OpenVM proof codec) is C and needs clang for the wasm32 target; the
# verifiers' getrandom backends are the shim's own. The module still declares a
# few wasm-bindgen host imports through OpenVM's dependencies, which a
# verification never calls; the Go runtime provides trapping stubs for them.
ENV CC_wasm32_unknown_unknown=clang
RUN RUSTFLAGS='--cfg getrandom_backend="custom"' \
    cargo build --release --target wasm32-unknown-unknown -p ere-verifier-wasm \
    && wasm-opt -Oz --strip-debug --strip-producers \
        -o /out.wasm target/wasm32-unknown-unknown/release/ere_verifier_wasm.wasm

FROM scratch AS artifact
COPY --from=build /out.wasm /ere-verifier.wasm
