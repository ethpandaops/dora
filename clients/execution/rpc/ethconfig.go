package rpc

import (
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const (
	DepositContract              = "DEPOSIT_CONTRACT_ADDRESS"
	ConsolidationRequestContract = "CONSOLIDATION_REQUEST_PREDEPLOY_ADDRESS"
	WithdrawalRequestContract    = "WITHDRAWAL_REQUEST_PREDEPLOY_ADDRESS"
)

type EthConfigFork struct {
	ActivationTime  uint64                    `json:"activationTime"`
	ChainID         string                    `json:"chainId"`
	ForkID          string                    `json:"forkId"`
	BlobSchedule    EthConfigBlobSchedule     `json:"blobSchedule"`
	Precompiles     map[string]common.Address `json:"precompiles"`
	SystemContracts map[string]common.Address `json:"systemContracts"`
}

type EthConfigBlobSchedule struct {
	Max                   uint64 `json:"max"`
	Target                uint64 `json:"target"`
	BaseFeeUpdateFraction uint64 `json:"baseFeeUpdateFraction"`
}

type EthConfig struct {
	Current *EthConfigFork `json:"current"`
	Next    *EthConfigFork `json:"next"`
	Last    *EthConfigFork `json:"last"`
}

func (f *EthConfigFork) CheckMismatch(f2 *EthConfigFork) []string {
	mismatches := []string{}

	if f.ActivationTime != f2.ActivationTime {
		mismatches = append(mismatches, "activationTime")
	}

	if f.ChainID != f2.ChainID {
		mismatches = append(mismatches, "chainId")
	}

	if f.ForkID != f2.ForkID {
		mismatches = append(mismatches, "forkId")
	}

	if f.BlobSchedule != f2.BlobSchedule {
		mismatches = append(mismatches, "blobSchedule")
	}

	if addressMismatches := checkAddressSet("precompiles", f.Precompiles, f2.Precompiles); len(addressMismatches) > 0 {
		mismatches = append(mismatches, addressMismatches...)
	}

	if addressMismatches := checkAddressSet("systemContracts", f.SystemContracts, f2.SystemContracts); len(addressMismatches) > 0 {
		mismatches = append(mismatches, addressMismatches...)
	}

	return mismatches
}

func checkAddressSet(prefix string, a1 map[string]common.Address, a2 map[string]common.Address) []string {
	mismatches := []string{}

	checked := map[string]bool{}
	for k, v := range a1 {
		if v2, exists := a2[k]; !exists || v != v2 {
			mismatches = append(mismatches, fmt.Sprintf("%s.%s", prefix, k))
		} else {
			checked[k] = true
		}
	}

	for k := range a2 {
		if !checked[k] {
			mismatches = append(mismatches, fmt.Sprintf("%s.%s", prefix, k))
		}
	}

	return mismatches
}

// GetActivationTime returns the activation time as a time.Time
func (f *EthConfigFork) GetActivationTime() time.Time {
	return time.Unix(int64(f.ActivationTime), 0)
}

// GetSystemContractAddress returns the address of a specific system contract
func (f *EthConfigFork) GetSystemContractAddress(contractType string) *common.Address {
	if f.SystemContracts == nil {
		return nil
	}

	if addr, exists := f.SystemContracts[contractType]; exists {
		return &addr
	}

	return nil
}
