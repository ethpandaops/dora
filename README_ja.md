# Beaconchainのエクスプローラー、Dora

<!-- hy-mt2-i18n:start -->
[English](./README.md) | [中文](./README_zh-CN.md) | **日本語** | [Español](./README_es.md)
<!-- hy-mt2-i18n:end -->


[![Badge](https://github.com/ethpandaops/dora/actions/workflows/build-master.yml/badge.svg)](https://github.com/ethpandaops/dora/actions?query=workflow%3A%22Build+master%22)
[![Go Report Card](https://goreportcard.com/badge/github.com/ethpandaops/dora)](https://goreportcard.com/report/github.com/ethpandaops/dora)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/ethpandaops/dora?label=Latest%20Release)](https://github.com/ethpandaops/dora/releases/latest)

## これは何ですか？
これは軽量なBeaconchainエクスプローラーです。

Beaconchainエクスプローラーとは、Ethereum Beacon Chain上のデータを閲覧したり操作したりできるツールです。これはブロックチェーンエクスプローラーに似ており、取引やブロックの現在の状態など、ブロックチェーン上のデータを確認できるものですが、Beaconchainの探索に特化しています。

この「軽量」なエクスプローラーは、ほとんどの情報を基盤となる標準的なビーコンノードAPIから直接読み込むため、運用がはるかに簡単でコストも低く抑えられます（Bigtableのようなサードパーティ製の専用データベースは不要です）。

## テストネットインスタンス
[Holešky](https://github.com/eth-clients/holesky) テストネット: 
* https://dora-holesky.pk910.de/
* https://dora.holesky.ethpandaops.io/

[Sepolia](https://github.com/eth-clients/sepolia) テストネット: 
* https://dora.sepolia.ethpandaops.io/

[Ephemery](https://github.com/ephemery-testnet/ephemery-resources) テストネット: 
* https://beaconlight.ephemery.dev/

# 設定と構成
設定および構成の手順については、[wiki](https://github.com/ethpandaops/dora/wiki)をご覧ください。

## 依存関係

このエクスプローラーには必須の外部依存関係はありません。メモリ内のみで完全に動作させることも可能です。\\
ただし、最適なパフォーマンスを得るためにはPostgreSQLデータベースの使用を推奨します。

# 開発環境のセットアップ

このリポジトリには、エクスプローラーの開発環境を簡単に構築できるスクリプトが含まれています。

ローカルで構築したDoraインスタンスを使用して完全なEthereumテストネットを起動するには、以下の手順に従ってください：

1. マシンにdockerおよび[kurtosis](https://docs.kurtosis.com/install)がインストールされていることを確認してください。
2. このリポジトリをクローンします。
3. `make devnet-run`を実行します。

`make devnet-run`コマンドを実行すると、複数のクライアントペアを持つkurtosisテストネットが起動します。開発作業が終了したらテストネットを停止するには、`make devnet-clean`を実行してください。

# サポートしてくださった方々へ

このエクスプローラーは、[gobitfly/eth2-beaconchain-explorer](https://github.com/gobitfly/eth2-beaconchain-explorer) のコードを大幅にベースにしています。

# ライセンス

[![ライセンス: GPL-3.0](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

