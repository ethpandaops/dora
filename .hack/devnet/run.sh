#!/bin/bash
DOCKER_DESKTOP=0
# Run docker version and grep for the Server line
server_line=$(docker version | grep Server)
# Check if "Docker Desktop" is in the server line
if [[ $server_line == *"Docker Desktop"* ]]; then
    echo "Server is running Docker Desktop"
    DOCKER_DESKTOP=1
else
    echo "Server is not running Docker Desktop"
fi

__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

## Run devnet using kurtosis
ENCLAVE_NAME="${ENCLAVE_NAME:-dora}"
if kurtosis enclave inspect "$ENCLAVE_NAME" > /dev/null; then
  echo "Kurtosis enclave '$ENCLAVE_NAME' is already up."
else
  kurtosis run github.com/ethpandaops/ethereum-package \
  --image-download always \
  --enclave "$ENCLAVE_NAME" \
  --args-file "${__dir}/kurtosis.devnet.config.yaml"
fi

# Get chain config
kurtosis files inspect "$ENCLAVE_NAME" el_cl_genesis_data ./config.yaml | tail -n +2 > "${__dir}/generated-chain-config.yaml"

## Generate Dora config
ENCLAVE_UUID=$(kurtosis enclave inspect "$ENCLAVE_NAME" --full-uuids | grep 'UUID:' | awk '{print $2}')

BEACON_NODES=$(docker ps -aq -f "label=enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=beacon" | tac)

EXECUTION_NODES=$(docker ps -aq -f "label=enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=execution" | tac)

# Conditionally execute this block
if [[ $DOCKER_DESKTOP -eq 1 ]]; then
  cat <<EOF > "${__dir}/generated-dora-config.yaml"
  logging:
    outputLevel: "info"
  chain:
    name: $ENCLAVE_NAME
    configPath: "${__dir}/generated-chain-config.yaml"
  server:
    host: "0.0.0.0"
    port: "8080"
  frontend:
    enabled: true
    debug: false
    pprof: true
    minimize: false
    siteName: "Dora the Explorer"
    siteSubtitle: "$ENCLAVE_NAME - Kurtosis"
    ethExplorerLink: ""
  beaconapi:
    localCacheSize: 10
    redisCacheAddr: ""
    redisCachePrefix: ""
    endpoints:
EOF

  # Append beacon nodes to the config file
  for node in $BEACON_NODES; do
      name=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}" $node)
      ip='127.0.0.1'
      port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "4000/tcp") 0).HostPort }}' $node)
      echo "    - { name: $name, url: http://$ip:$port }" >> "${__dir}/generated-dora-config.yaml"
  done

  cat <<EOF >> "${__dir}/generated-dora-config.yaml"
  executionapi:
    depositLogBatchSize: 1000
    endpoints:
EOF

  # Append execution nodes to the config file
  for node in $EXECUTION_NODES; do
      name=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}" $node)
      ip='127.0.0.1'
      port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "8545/tcp") 0).HostPort }}' $node)
      echo "    - { name: $name, url: http://$ip:$port }" >> "${__dir}/generated-dora-config.yaml"
  done

  cat <<EOF >> "${__dir}/generated-dora-config.yaml"
  indexer:
    inMemoryEpochs: 8
    cachePersistenceDelay: 8
    disableIndexWriter: false
    syncEpochCooldown: 1
  database:
    engine: "sqlite"
    sqlite:
      file: "${__dir}/generated-database.sqlite"
EOF

  cat <<EOF
  ============================================================================================================
  Dora config at ${__dir}/generated-dora-config.yaml
  Chain config at ${__dir}/generated-chain-config.yaml
  Database at ${__dir}/generated-database.sqlite
  ============================================================================================================
EOF
else
  echo "hello"
fi