#!/bin/bash
__dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

## Run devnet using kurtosis
ENCLAVE_NAME="${ENCLAVE_NAME:-dora}"
if kurtosis enclave inspect "$ENCLAVE_NAME" > /dev/null; then
  echo "Kurtosis enclave '$ENCLAVE_NAME' is already up."
else
  kurtosis run github.com/ethpandaops/ethereum-package --enclave "$ENCLAVE_NAME" --args-file "${__dir}/kurtosis.devnet.config.yaml"
fi

# Get chain config
kurtosis files inspect dora el_cl_genesis_data ./config.yaml | tail -n +2 > "${__dir}/generated-chain-config.yaml"

## Generate Dora config
ENCLAVE_UUID=$(kurtosis enclave inspect "$ENCLAVE_NAME" --full-uuids | grep 'UUID:' | awk '{print $2}')

BEACON_NODDES=$(docker ps -aq -f "label=enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=beacon" | tac)

EXECUTION_NODES=$(docker ps -aq -f "label=enclave_uuid=$ENCLAVE_UUID" \
              -f "label=com.kurtosistech.app-id=kurtosis" \
              -f "label=com.kurtosistech.custom.ethereum-package.client-type=execution" | tac)

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
$(docker inspect -f "    - { name: {{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}, url: http://{{ with index .NetworkSettings.Networks \"kt-$ENCLAVE_NAME\"}}{{.IPAddress }}:4000{{end}} }" $BEACON_NODDES)
executionapi:
  depositLogBatchSize: 1000
  endpoints:
$(docker inspect -f "    - { name: {{ with index .Config.Labels \"com.kurtosistech.id\"}}{{.}}{{end}}, url: http://{{ with index .NetworkSettings.Networks \"kt-$ENCLAVE_NAME\"}}{{.IPAddress }}:8545{{end}} }"  $EXECUTION_NODES)
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
