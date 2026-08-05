# Dora, el explorador de Beaconchain

<!-- hy-mt2-i18n:start -->
[English](./README.md) | [中文](./README_zh-CN.md) | [日本語](./README_ja.md) | **Español**
<!-- hy-mt2-i18n:end -->


[![Insignia](https://github.com/ethpandaops/dora/actions/workflows/build-master.yml/badge.svg)](https://github.com/ethpandaops/dora/actions?query=workflow%3A%22Build+master%22)
[![Calificación de Go](https://goreportcard.com/badge/github.com/ethpandaops/dora)](https://goreportcard.com/report/github.com/ethpandaops/dora)
[![Versión reciente en GitHub](https://img.shields.io/github/v/release/ethpandaops/dora?label=Latest%20Release)](https://github.com/ethpandaops/dora/releases/latest)

## ¿Qué es esto?
Se trata de un explorador de Beaconchain ligero.

Un explorador de Beaconchain es una herramienta que permite a los usuarios ver e interactuar con los datos de la Ethereum Beacon Chain. Es similar a un explorador de blockchain, el cual permite a los usuarios consultar datos de una cadena de bloques como el estado actual de las transacciones y los bloques, pero se centra específicamente en la exploración de la beaconchain.

Este explorador “ligero” carga la mayor parte de la información directamente desde una API estándar de nodo beacon subyacente, lo que hace que su funcionamiento sea mucho más sencillo y económico (no se requiere ninguna base de datos propietaria de terceros como BigTables).

## Instancias de red de pruebas
[Holešky](https://github.com/eth-clients/holesky) Red de pruebas: 
* https://dora-holesky.pk910.de/
* https://dora.holesky.ethpandaops.io/

[Sepolia](https://github.com/eth-clients/sepolia) Testnet: 
* https://dora.sepolia.ethpandaops.io/

Testnet [Ephemery](https://github.com/ephemery-testnet/ephemery-resources): 
* https://beaconlight.ephemery.dev/

# Configuración e instalación
Lea la [wiki](https://github.com/ethpandaops/dora/wiki) para obtener instrucciones de configuración e instalación.

## Dependencias

El explorador no tiene dependencias externas obligatorias. Incluso puede ejecutarse completamente en memoria solamente.  
Sin embargo, para obtener el mejor rendimiento, recomiendo utilizar una base de datos PostgreSQL.

# Configuración de desarrollo

El repositorio contiene un script que simplifica la creación de un entorno de desarrollo para el explorador.

Sigue estos pasos para configurar una red de prueba completa de Ethereum con la instancia Dora compilada localmente:

1. Asegúrese de que Docker y [kurtosis](https://docs.kurtosis.com/install) estén instalados en su equipo.  
2. Clone el repositorio.  
3. Ejecute `make devnet-run`.

La orden `make devnet-run` inicia una red de prueba de Kurtosis con múltiples pares de clientes. Para detener la red de prueba una vez finalizada la fase de desarrollo, ejecute `make devnet-clean`.

# Agradecimientos

Este explorador se basa en gran medida en el código de [gobitfly/eth2-beaconchain-explorer](https://github.com/gobitfly/eth2-beaconchain-explorer).

# Licencia

[![Licencia: GPL-3.0](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

