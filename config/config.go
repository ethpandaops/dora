package config

import _ "embed"

//go:embed default.config.yml
var DefaultConfigYml string

//go:embed preset-mainnet.chain.yml
var MainnetPresetYml string

//go:embed preset-minimal.chain.yml
var MinimalPresetYml string

//go:embed mainnet.chain.yml
var MainnetChainYml string

//go:embed prater.chain.yml
var PraterChainYml string

//go:embed sepolia.chain.yml
var SepoliaChainYml string

//go:embed testnet.chain.yml
var TestnetChainYml string
