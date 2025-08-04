package execution

import (
	"fmt"
	"strconv"
	"time"
)

type EthConfigFork struct {
	ActivationTime  uint64 `json:"activationTime"`
	ChainID         string `json:"chainId"`
	ForkID          string `json:"forkId"`
	BlobSchedule    map[string]interface{} `json:"blobSchedule"` // TODO: parse this properly
	Precompiles     map[string]interface{} `json:"precompiles"`  // TODO: parse this properly
	SystemContracts *SystemContracts       `json:"systemContracts"`
}

type SystemContracts struct {
	DepositContract      string `json:"depositContract"`
	WithdrawalContract   string `json:"withdrawalContract"`
	ConsolidationContract string `json:"consolidationContract"`
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

	// Parse blob schedule (keep as map for now, TODO: proper parsing)
	if blobSchedule, ok := raw["blobSchedule"].(map[string]interface{}); ok {
		fork.BlobSchedule = blobSchedule
	}

	// Parse precompiles (keep as map for now, TODO: proper parsing)
	if precompiles, ok := raw["precompiles"].(map[string]interface{}); ok {
		fork.Precompiles = precompiles
	}

	// Parse system contracts
	if systemContracts, ok := raw["systemContracts"].(map[string]interface{}); ok {
		contracts, err := parseSystemContracts(systemContracts)
		if err != nil {
			return nil, fmt.Errorf("failed to parse system contracts: %w", err)
		}
		fork.SystemContracts = contracts
	}

	return fork, nil
}

func parseSystemContracts(raw map[string]interface{}) (*SystemContracts, error) {
	contracts := &SystemContracts{}

	if deposit, ok := raw["depositContract"].(string); ok {
		contracts.DepositContract = deposit
	}

	if withdrawal, ok := raw["withdrawalContract"].(string); ok {
		contracts.WithdrawalContract = withdrawal
	}

	if consolidation, ok := raw["consolidationContract"].(string); ok {
		contracts.ConsolidationContract = consolidation
	}

	return contracts, nil
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

	switch contractType {
	case "deposit":
		return f.SystemContracts.DepositContract
	case "withdrawal":
		return f.SystemContracts.WithdrawalContract
	case "consolidation":
		return f.SystemContracts.ConsolidationContract
	default:
		return ""
	}
}