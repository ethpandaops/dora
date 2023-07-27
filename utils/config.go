package utils

import (
	"fmt"
	"os"

	"dario.cat/mergo"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/pk910/light-beaconchain-explorer/config"
	"github.com/pk910/light-beaconchain-explorer/types"
)

// Config is the globally accessible configuration
var Config *types.Config

// ReadConfig will process a configuration
func ReadConfig(cfg *types.Config, path string) error {
	err := readConfigFile(cfg, path)
	if err != nil {
		return err
	}

	readConfigEnv(cfg)

	var chainConfig types.ChainConfig
	if cfg.Chain.ConfigPath == "" {
		switch cfg.Chain.Name {
		case "mainnet":
			err = yaml.Unmarshal([]byte(config.MainnetChainYml), &chainConfig)
		case "prater":
			err = yaml.Unmarshal([]byte(config.PraterChainYml), &chainConfig)
		case "sepolia":
			err = yaml.Unmarshal([]byte(config.SepoliaChainYml), &chainConfig)
		default:
			return fmt.Errorf("tried to set known chain-config, but unknown chain-name")
		}
		if err != nil {
			return err
		}
	} else {
		f, err := os.Open(cfg.Chain.ConfigPath)
		if err != nil {
			return fmt.Errorf("error opening Chain Config file %v: %w", cfg.Chain.ConfigPath, err)
		}
		decoder := yaml.NewDecoder(f)
		err = decoder.Decode(&chainConfig)
		if err != nil {
			return fmt.Errorf("error decoding Chain Config file %v: %v", cfg.Chain.ConfigPath, err)
		}
	}

	// load preset if PresetBase is set
	if chainConfig.PresetBase != "" {
		var chainPreset types.ChainConfig
		switch chainConfig.PresetBase {
		case "mainnet":
			err = yaml.Unmarshal([]byte(config.MainnetPresetYml), &chainPreset)
		case "minimal":
			err = yaml.Unmarshal([]byte(config.MinimalPresetYml), &chainPreset)
		default:
			return fmt.Errorf("tried to use unknown chain-preset: %v", chainConfig.PresetBase)
		}
		if err != nil {
			return err
		}

		err := mergo.Merge(&chainPreset, chainConfig, mergo.WithOverride)
		if err != nil {
			return fmt.Errorf("error merging chain preset: %v", err)
		}
		cfg.Chain.Config = chainPreset
	} else {
		cfg.Chain.Config = chainConfig
	}

	cfg.Chain.Name = cfg.Chain.Config.ConfigName

	if cfg.Chain.GenesisTimestamp == 0 {
		switch cfg.Chain.Name {
		case "mainnet":
			cfg.Chain.GenesisTimestamp = 1606824023
		case "prater":
			cfg.Chain.GenesisTimestamp = 1616508000
		case "sepolia":
			cfg.Chain.GenesisTimestamp = 1655733600
		default:
			return fmt.Errorf("tried to set known genesis-timestamp, but unknown chain-name")
		}
	}

	if cfg.Chain.GenesisValidatorsRoot == "" {
		switch cfg.Chain.Name {
		case "mainnet":
			cfg.Chain.GenesisValidatorsRoot = "0x4b363db94e286120d76eb905340fdd4e54bfe9f06bf33ff6cf5ad27f511bfe95"
		case "prater":
			cfg.Chain.GenesisValidatorsRoot = "0x043db0d9a83813551ee2f33450d23797757d430911a9320530ad8a0eabc43efb"
		case "sepolia":
			cfg.Chain.GenesisValidatorsRoot = "0xd8ea171f3c94aea21ebc42a1ed61052acf3f9209c00e4efbaaddac09ed9b8078"
		default:
			return fmt.Errorf("tried to set known genesis-validators-root, but unknown chain-name")
		}
	}

	log.WithFields(log.Fields{
		"genesisTimestamp":       cfg.Chain.GenesisTimestamp,
		"genesisValidatorsRoot":  cfg.Chain.GenesisValidatorsRoot,
		"configName":             cfg.Chain.Config.ConfigName,
		"depositChainID":         cfg.Chain.Config.DepositChainID,
		"depositNetworkID":       cfg.Chain.Config.DepositNetworkID,
		"depositContractAddress": cfg.Chain.Config.DepositContractAddress,
	}).Infof("did init config")

	return nil
}

func readConfigFile(cfg *types.Config, path string) error {
	if path == "" {
		return yaml.Unmarshal([]byte(config.DefaultConfigYml), cfg)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error opening config file %v: %v", path, err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(cfg)
	if err != nil {
		return fmt.Errorf("error decoding config file %v: %v", path, err)
	}

	return nil
}

func readConfigEnv(cfg *types.Config) error {
	return envconfig.Process("", cfg)
}
