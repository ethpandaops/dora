#!/bin/bash -x
__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENCLAVE_NAME="${ENCLAVE_NAME:-dora}"
kurtosis enclave rm -f "$ENCLAVE_NAME"

echo "Cleaning up generated files..."
rm -f ${__dir}/generated-*
