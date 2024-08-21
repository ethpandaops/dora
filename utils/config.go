package utils

import (
	"fmt"
	"net/url"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"

	"github.com/ethpandaops/dora/config"
	"github.com/ethpandaops/dora/types"
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

	// endpoints
	if cfg.BeaconApi.Endpoints == nil && cfg.BeaconApi.Endpoint != "" {
		cfg.BeaconApi.Endpoints = []types.EndpointConfig{
			{
				Url:  cfg.BeaconApi.Endpoint,
				Name: "default",
			},
		}
	}
	for idx, endpoint := range cfg.BeaconApi.Endpoints {
		if endpoint.Name == "" {
			url, _ := url.Parse(endpoint.Url)
			if url != nil {
				cfg.BeaconApi.Endpoints[idx].Name = url.Hostname()
			} else {
				cfg.BeaconApi.Endpoints[idx].Name = fmt.Sprintf("endpoint-%v", idx+1)
			}
		}
	}
	if len(cfg.BeaconApi.Endpoints) == 0 {
		return fmt.Errorf("missing beacon node endpoints (need at least 1 endpoint to run the explorer)")
	}

	// execution endpoints
	if cfg.ExecutionApi.Endpoints == nil && cfg.ExecutionApi.Endpoint != "" {
		cfg.ExecutionApi.Endpoints = []types.EndpointConfig{
			{
				Url:  cfg.ExecutionApi.Endpoint,
				Name: "default",
			},
		}
	}
	for idx, endpoint := range cfg.ExecutionApi.Endpoints {
		if endpoint.Name == "" {
			url, _ := url.Parse(endpoint.Url)
			if url != nil {
				cfg.ExecutionApi.Endpoints[idx].Name = url.Hostname()
			} else {
				cfg.ExecutionApi.Endpoints[idx].Name = fmt.Sprintf("endpoint-%v", idx+1)
			}
		}
	}

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
		return fmt.Errorf("error decoding explorer config: %v", err)
	}
	return nil
}

func readConfigEnv(cfg *types.Config) error {
	return envconfig.Process("", cfg)
}
