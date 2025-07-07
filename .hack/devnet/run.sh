#!/bin/bash
__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ -f "${__dir}/custom-kurtosis.devnet.config.yaml" ]; then
  config_file="${__dir}/custom-kurtosis.devnet.config.yaml"
else
  config_file="${__dir}/kurtosis.devnet.config.yaml"
fi

## Run devnet using kurtosis
ENCLAVE_NAME="${ENCLAVE_NAME:-dora}"
ETHEREUM_PACKAGE="${ETHEREUM_PACKAGE:-github.com/ethpandaops/ethereum-package}"
if kurtosis enclave inspect "$ENCLAVE_NAME" > /dev/null; then
  echo "Kurtosis enclave '$ENCLAVE_NAME' is already up."
else
  kurtosis run "$ETHEREUM_PACKAGE" \
  --image-download always \
  --enclave "$ENCLAVE_NAME" \
  --args-file "${config_file}"
fi

# Get validator ranges
kurtosis files inspect "$ENCLAVE_NAME" validator-ranges validator-ranges.yaml | tail -n +2 > "${__dir}/generated-validator-ranges.yaml"

# Get dora config
kurtosis files inspect "$ENCLAVE_NAME" dora-config dora-config.yaml | tail -n +2 > "${__dir}/generated-dora-kt-config.yaml"

## Generate Dora config
ENCLAVE_UUID=$(kurtosis enclave inspect "$ENCLAVE_NAME" --full-uuids | grep 'UUID:' | awk '{print $2}')

BEACON_NODES=$(docker ps -aq -f "label=kurtosis_enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=beacon" | tac)

EXECUTION_NODES=$(docker ps -aq -f "label=kurtosis_enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=execution" | tac)

cat <<EOF > "${__dir}/generated-dora-config.yaml"
logging:
  outputLevel: "info"
server:
  host: "0.0.0.0"
  port: "8080"
frontend:
  enabled: true
  debug: true
  pprof: true
  minimize: false
  siteName: "Dora the Explorer"
  siteSubtitle: "$ENCLAVE_NAME - Kurtosis"
  ethExplorerLink: ""
  publicRpcUrl: "$(
  for node in $EXECUTION_NODES; do
    ip=$(echo '127.0.0.1')
    port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "8545/tcp") 0).HostPort }}' $node)
    if [ -z "$port" ]; then
      continue
    fi
    echo "http://$ip:$port"
    break
  done
  )"
  rainbowkitProjectId: "15fe4ab4d5c0bcb6f0dc7c398301ff0e"
  validatorNamesYaml: "${__dir}/generated-validator-ranges.yaml"
  showSensitivePeerInfos: true
  showSubmitDeposit: true
  showSubmitElRequests: true
api:
  enabled: true
  corsOrigins:
    - "*"
beaconapi:
  localCacheSize: 10
  redisCacheAddr: ""
  redisCachePrefix: ""
  endpoints:
$(for node in $BEACON_NODES; do
    name=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}" $node)
    ip=$(echo '127.0.0.1')
    port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "3500/tcp") 0).HostPort }}' $node 2>/dev/null)
    if [ -z "$port" ]; then
      port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "4000/tcp") 0).HostPort }}' $node)
    fi
    if [ -z "$port" ]; then
      port="65535"
    fi
    echo "    - { name: $name, url: http://$ip:$port }"
done)
executionapi:
  depositLogBatchSize: 1000
  endpoints:
$(for node in $EXECUTION_NODES; do
    name=$(docker inspect -f "{{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}" $node)
    ip=$(echo '127.0.0.1')
    port=$(docker inspect --format='{{ (index (index .NetworkSettings.Ports "8545/tcp") 0).HostPort }}' $node)
    if [ -z "$port" ]; then
      port="65535"
    fi
    echo "    - { name: $name, url: http://$ip:$port }"
done)
indexer:
  inMemoryEpochs: 8
  activityHistoryLength: 6
  cachePersistenceDelay: 8
  disableIndexWriter: false
  syncEpochCooldown: 1
database:
  engine: "sqlite"
  sqlite:
    file: "${__dir}/generated-database.sqlite"
EOF

if [ -f ${__dir}/generated-dora-kt-config.yaml ]; then
  fullcfg=$(yq eval-all 'select(fileIndex == 0) as $target | select(fileIndex == 1) as $source | $target.executionapi.endpoints = $source.executionapi.endpoints | $target' ${__dir}/generated-dora-config.yaml ${__dir}/generated-dora-kt-config.yaml)
  if [ ! -z "$fullcfg" ]; then
    echo "$fullcfg" > ${__dir}/generated-dora-config.yaml
    rm ${__dir}/generated-dora-kt-config.yaml
  fi
fi


cat <<EOF
============================================================================================================
Dora config at ${__dir}/generated-dora-config.yaml
Chain config at ${__dir}/generated-chain-config.yaml
Database at ${__dir}/generated-database.sqlite
============================================================================================================
EOF
