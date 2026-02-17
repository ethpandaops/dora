#!/bin/bash -x
__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENCLAVE_NAME="${ENCLAVE_NAME:-dora}"
kurtosis enclave rm -f "$ENCLAVE_NAME"

# Clear S3 data if blockdb settings and generated config exist
if [ -f "${__dir}/custom-blockdb-settings.yaml" ] && [ -f "${__dir}/generated-dora-config.yaml" ]; then
  echo "Clearing S3 blockdb data..."
  DORA_UTILS="go run ${__dir}/../../cmd/dora-utils/"
  $DORA_UTILS s3-clear -c "${__dir}/generated-dora-config.yaml" || echo "Warning: S3 clear failed (may not be critical)"
fi

echo "Cleaning up generated files..."
rm -f ${__dir}/generated-*
