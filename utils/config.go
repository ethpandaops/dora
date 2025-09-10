package utils

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

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

	// Load beacon endpoints from URL if specified
	if cfg.BeaconApi.EndpointsURL != "" && cfg.BeaconApi.Endpoints == nil {
		endpoints, err := loadEndpointsFromUrl(cfg.BeaconApi.EndpointsURL)
		if err != nil {
			return fmt.Errorf("failed to load beacon endpoints from URL: %w", err)
		}
		cfg.BeaconApi.Endpoints = endpoints
	}

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

	// Load execution endpoints from URL if specified
	if cfg.ExecutionApi.EndpointsURL != "" && cfg.ExecutionApi.Endpoints == nil {
		endpoints, err := loadEndpointsFromUrl(cfg.ExecutionApi.EndpointsURL)
		if err != nil {
			return fmt.Errorf("failed to load execution endpoints from URL: %w", err)
		}
		cfg.ExecutionApi.Endpoints = endpoints
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

// loadEndpointsFromUrl loads endpoint configurations from a URL or file path
func loadEndpointsFromUrl(endpointsURL string) ([]types.EndpointConfig, error) {
	var data []byte
	var err error

	// Check if it's a URL or a file path
	if strings.HasPrefix(endpointsURL, "http://") || strings.HasPrefix(endpointsURL, "https://") {
		// Load from HTTP URL
		resp, err := http.Get(endpointsURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch endpoints from URL %s: %w", endpointsURL, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch endpoints from URL %s: status code %d", endpointsURL, resp.StatusCode)
		}

		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body from URL %s: %w", endpointsURL, err)
		}
	} else {
		// Load from file
		data, err = os.ReadFile(endpointsURL)
		if err != nil {
			return nil, fmt.Errorf("failed to read endpoints file %s: %w", endpointsURL, err)
		}
	}

	// Parse YAML data
	var endpoints []types.EndpointConfig
	err = yaml.Unmarshal(data, &endpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoints YAML: %w", err)
	}

	return endpoints, nil
}
