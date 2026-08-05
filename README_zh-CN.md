# Beaconchain 探索器 Dora

<!-- hy-mt2-i18n:start -->
[English](./README.md) | **中文** | [日本語](./README_ja.md) | [Español](./README_es.md)
<!-- hy-mt2-i18n:end -->


[![徽章](https://github.com/ethpandaops/dora/actions/workflows/build-master.yml/badge.svg)](https://github.com/ethpandaops/dora/actions?query=workflow%3A%22Build+master%22)
[![Go 项目质量报告卡](https://goreportcard.com/badge/github.com/ethpandaops/dora)](https://goreportcard.com/report/github.com/ethpandaops/dora)
[![GitHub 发布版本（最新按时间排序）](https://img.shields.io/github/v/release/ethpandaops/dora?label=Latest%20Release)](https://github.com/ethpandaops/dora/releases/latest)

## 这是什么？
这是一个轻量级的 Beaconchain 探索器。

Beaconchain浏览器是一种能让用户查看并操作Ethereum Beacon Chain上数据的工具。它与区块链浏览器类似，后者可让用户查看区块链上的数据，比如交易和区块的当前状态——只不过这款工具专为探索Beaconchain而设计。

这款“轻量级”的浏览器直接从底层的标准信标节点API加载大部分信息，因此运行起来更加便捷且成本更低（无需使用BigTables之类的第三方专有数据库）。

## 测试网节点
[Holešky](https://github.com/eth-clients/holesky) 测试网：
* https://dora-holesky.pk910.de/
* https://dora.holesky.ethpandaops.io/

[Sepolia](https://github.com/eth-clients/sepolia) 测试网： 
* https://dora.sepolia.ethpandaops.io/

[Ephemery](https://github.com/ephemery-testnet/ephemery-resources) 测试网： 
* https://beaconlight.ephemery.dev/

# 设置与配置
请查阅[Wiki文档](https://github.com/ethpandaops/dora/wiki)，了解相关的设置与配置指南。

## 依赖项

该浏览器没有强制性的外部依赖项，甚至完全可以仅在内存中运行。  
不过，为获得最佳性能，建议使用 PostgreSQL 数据库。

# 开发环境配置

该仓库中包含一个脚本，可简化探索器开发环境的搭建流程。

按照以下步骤，使用本地构建的 Dora 实例搭建完整的以太坊测试网：

1. 确保您的机器上已安装 Docker 以及 [kurtosis](https://docs.kurtosis.com/install)。
2. 克隆该仓库。
3. 运行 `make devnet-run`。

`make devnet-run` 命令会启动一个包含多对客户端节点的 kurtosis 测试网。在完成开发工作后，若要停止该测试网，请运行 `make devnet-clean`。

# 致谢

该浏览器在很大程度上基于 [gobitfly/eth2-beaconchain-explorer](https://github.com/gobitfly/eth2-beaconchain-explorer) 的代码开发而成。

# 许可证

[![许可证：GPL-3.0](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

