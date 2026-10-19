#!/usr/bin/env bash
# Builds the embedded zkVM verifier module for an ere release tag in Docker and
# stores it snappy-compressed in wasm/:
#
#   ./build.sh v0.17.2   ->  wasm/ere-v0.17.2.wasm.sz
#
# Register the tag in generations.go afterwards (newest first).
set -euo pipefail

tag="${1:-}"
if [ -z "$tag" ]; then
    echo "usage: $0 <ere tag>" >&2
    exit 1
fi

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$dir/../.." && pwd)"
out="$dir/wasm/ere-$tag.wasm.sz"
tmp="$(mktemp -d -p "${TMPDIR:-/var/tmp}")"
trap 'rm -rf "$tmp"' EXIT

docker build --build-arg "ERE_TAG=$tag" --target artifact --output "type=local,dest=$tmp" "$dir"
(cd "$repo" && go run ./executionproof/verifiers/pack "$tmp/ere-verifier.wasm" "$out")

echo "wrote $out ($(stat -c%s "$out") bytes, $(stat -c%s "$tmp/ere-verifier.wasm") bytes uncompressed)"
