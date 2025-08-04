package execution

import (
	"fmt"
	"strconv"
	"time"
)

type EthConfigFork struct {
	ActivationTime  uint64                 `json:"activationTime"`
	ChainID         string                 `json:"chainId"`
	ForkID          string                 `json:"forkId"`
	BlobSchedule    map[string]string `json:"blobSchedule"`
	Precompiles     map[string]string `json:"precompiles"`
	SystemContracts map[string]string       `json:"systemContracts"`
}


type EthConfig struct {
	Current *EthConfigFork `json:"current"`
	Next    *EthConfigFork `json:"next"`
	Last    *EthConfigFork `json:"last"`
}

// ParseEthConfig parses the raw eth_config response into a structured EthConfig
func ParseEthConfig(rawConfig map[string]interface{}) (*EthConfig, error) {
	if rawConfig == nil {
		return nil, nil
	}

	config := &EthConfig{}

	if current, ok := rawConfig["current"].(map[string]interface{}); ok {
		parsed, err := parseEthConfigFork(current)
		if err != nil {
			return nil, fmt.Errorf("failed to parse current config: %w", err)
		}
		config.Current = parsed
	}

	if next, ok := rawConfig["next"].(map[string]interface{}); ok {
		parsed, err := parseEthConfigFork(next)
		if err != nil {
			return nil, fmt.Errorf("failed to parse next config: %w", err)
		}
		config.Next = parsed
	}

	if last, ok := rawConfig["last"].(map[string]interface{}); ok {
		parsed, err := parseEthConfigFork(last)
		if err != nil {
			return nil, fmt.Errorf("failed to parse last config: %w", err)
		}
		config.Last = parsed
	}

	return config, nil
}

func parseEthConfigFork(raw map[string]interface{}) (*EthConfigFork, error) {
	fork := &EthConfigFork{}

	// Parse activation time
	if activationTime, ok := raw["activationTime"]; ok {
		switch v := activationTime.(type) {
		case float64:
			fork.ActivationTime = uint64(v)
		case string:
			parsed, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse activationTime: %w", err)
			}
			fork.ActivationTime = parsed
		}
	}

	// Parse chain ID
	if chainID, ok := raw["chainId"].(string); ok {
		fork.ChainID = chainID
	}

	// Parse fork ID
	if forkID, ok := raw["forkId"].(string); ok {
		fork.ForkID = forkID
	}

	// Parse blob schedule
	if blobSchedule, ok := raw["blobSchedule"].(map[string]interface{}); ok {
		fork.BlobSchedule = parseStringMap(blobSchedule)
	}

	// Parse precompiles
	if precompiles, ok := raw["precompiles"].(map[string]interface{}); ok {
		fork.Precompiles = parseStringMap(precompiles)
	}

	// Parse system contracts
	if systemContracts, ok := raw["systemContracts"].(map[string]interface{}); ok {
		fork.SystemContracts = parseStringMap(systemContracts)
	}

	return fork, nil
}

func parseStringMap(raw map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for key, value := range raw {
		if str, ok := value.(string); ok {
			result[key] = str
		}
	}
	return result
}

// GetActivationTime returns the activation time as a time.Time
func (f *EthConfigFork) GetActivationTime() time.Time {
	return time.Unix(int64(f.ActivationTime), 0)
}

// GetSystemContractAddress returns the address of a specific system contract
func (f *EthConfigFork) GetSystemContractAddress(contractType string) string {
	if f.SystemContracts == nil {
		return ""
	}

	// Map common contract type names to their actual keys
	keyMap := map[string]string{
		"deposit":       "depositContract",
		"withdrawal":    "withdrawalContract",
		"consolidation": "consolidationContract",
	}

	if key, exists := keyMap[contractType]; exists {
		return f.SystemContracts[key]
	}

	// Also allow direct key lookup for flexibility
	return f.SystemContracts[contractType]
}
