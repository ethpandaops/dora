package execution

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethpandaops/dora/clients/execution/rpc"
)

var DefaultSystemContractAddresses = map[string]common.Address{
	rpc.ConsolidationRequestContract: common.HexToAddress("0x0000BBdDc7CE488642fb579F8B00f3a590007251"),
	rpc.WithdrawalRequestContract:    common.HexToAddress("0x00000961Ef480Eb55e80D19ad83579A64c007002"),
}

type ChainState struct {
	specMutex         sync.RWMutex
	specs             *rpc.ChainSpec
	clientConfigMutex sync.RWMutex
	clientConfig      rpc.EthConfig
	config            *core.Genesis
}

func newChainState() *ChainState {
	return &ChainState{}
}

func (cache *ChainState) SetClientSpecs(specs *rpc.ChainSpec) error {
	cache.specMutex.Lock()
	defer cache.specMutex.Unlock()

	if cache.specs != nil {
		mismatches := cache.specs.CheckMismatch(specs)
		if len(mismatches) > 0 {
			return fmt.Errorf("spec mismatch: %v", strings.Join(mismatches, ", "))
		}
	}

	cache.specs = specs

	return nil
}

func (cache *ChainState) SetClientConfig(config *rpc.EthConfig) ([]error, error) {
	cache.clientConfigMutex.Lock()
	defer cache.clientConfigMutex.Unlock()

	if config.Current == nil {
		return nil, fmt.Errorf("current config is nil")
	}

	warnings := []error{}

	currentConfig := config.Current
	nextConfig := config.Next
	lastConfig := config.Last

	if cache.clientConfig.Current != nil {
		// check current config
		mismatches := cache.clientConfig.Current.CheckMismatch(currentConfig)
		if len(mismatches) > 0 && cache.clientConfig.Current.ActivationTime < currentConfig.ActivationTime && currentConfig.ActivationTime <= uint64(time.Now().Unix()) {
			// current config changed
			cache.clientConfig.Current = currentConfig
			cache.clientConfig.Next = nextConfig
		} else if len(mismatches) > 0 {
			return nil, fmt.Errorf("client config mismatch: %v", strings.Join(mismatches, ", "))
		}
	} else {
		cache.clientConfig.Current = currentConfig
	}

	if cache.clientConfig.Next != nil {
		if nextConfig == nil && cache.clientConfig.Next != nil {
			warnings = append(warnings, fmt.Errorf("next config is nil"))
		} else if nextConfig != nil && cache.clientConfig.Next == nil {
			cache.clientConfig.Next = nextConfig
		} else if nextConfig != nil {
			if nextMismatches := cache.clientConfig.Next.CheckMismatch(nextConfig); len(nextMismatches) > 0 {
				if nextConfig.ActivationTime < cache.clientConfig.Next.ActivationTime && nextConfig.ActivationTime > uint64(time.Now().Unix()) {
					cache.clientConfig.Next = nextConfig
				} else {
					warnings = append(warnings, fmt.Errorf("next config mismatch: %v", strings.Join(nextMismatches, ", ")))
				}
			}
		}
	} else {
		cache.clientConfig.Next = nextConfig
	}

	if cache.clientConfig.Last != nil {
		if lastConfig == nil && cache.clientConfig.Last != nil {
			warnings = append(warnings, fmt.Errorf("last config is nil"))
		} else if lastConfig != nil && cache.clientConfig.Last == nil {
			cache.clientConfig.Last = lastConfig
		} else if lastConfig != nil {
			if lastMismatches := cache.clientConfig.Last.CheckMismatch(lastConfig); len(lastMismatches) > 0 {
				if lastConfig.ActivationTime > cache.clientConfig.Last.ActivationTime {
					cache.clientConfig.Last = lastConfig
				} else {
					warnings = append(warnings, fmt.Errorf("last config mismatch: %v", strings.Join(lastMismatches, ", ")))
				}
			}
		}
	} else {
		cache.clientConfig.Last = lastConfig
	}

	return warnings, nil
}

func (cache *ChainState) GetSpecs() *rpc.ChainSpec {
	cache.specMutex.RLock()
	defer cache.specMutex.RUnlock()

	return cache.specs
}

func (cache *ChainState) GetChainID() *big.Int {
	cache.specMutex.RLock()
	defer cache.specMutex.RUnlock()

	if cache.specs == nil {
		return nil
	}

	chainID := new(big.Int)

	_, ok := chainID.SetString(cache.specs.ChainID, 10)
	if !ok {
		return nil
	}

	return chainID
}

func (cache *ChainState) getCurrentEthConfig() *rpc.EthConfigFork {
	cache.clientConfigMutex.RLock()
	defer cache.clientConfigMutex.RUnlock()

	if cache.clientConfig.Current == nil {
		return nil
	}

	currentConfig := cache.clientConfig.Current

	if cache.clientConfig.Next != nil && time.Since(currentConfig.GetActivationTime()) < time.Second*10 {
		return cache.clientConfig.Next
	}

	return cache.clientConfig.Current
}

func (cache *ChainState) GetSystemContractAddress(systemContract string) common.Address {
	var addr *common.Address

	currentConfig := cache.getCurrentEthConfig()
	if currentConfig != nil {
		addr = currentConfig.GetSystemContractAddress(systemContract)
	}

	if addr != nil {
		return *addr
	}

	return DefaultSystemContractAddresses[systemContract]
}

func (cache *ChainState) SetGenesisConfig(genesis *core.Genesis) {
	cache.config = genesis
}

func (cache *ChainState) GetGenesisConfig() *core.Genesis {
	return cache.config
}

func (cache *ChainState) GetClientConfig() *rpc.EthConfig {
	cache.clientConfigMutex.RLock()
	defer cache.clientConfigMutex.RUnlock()
	return &cache.clientConfig
}

func (cache *ChainState) GetBlobScheduleForTimestamp(timestamp time.Time) *rpc.EthConfigBlobSchedule {
	forkSchedule := cache.getBlobScheduleForTimestampFromConfig(timestamp)
	if forkSchedule == nil && cache.config != nil {
		// extract fork schedule from genesis config
		forkSchedules := cache.GetFullBlobSchedule()
		for _, schedule := range forkSchedules {
			if schedule.Timestamp.Before(timestamp) {
				forkSchedule = &schedule.Schedule
			}
		}
	}

	return forkSchedule
}

func (cache *ChainState) getBlobScheduleForTimestampFromConfig(timestamp time.Time) *rpc.EthConfigBlobSchedule {
	cache.clientConfigMutex.RLock()
	defer cache.clientConfigMutex.RUnlock()

	if cache.clientConfig.Current == nil {
		return nil
	}

	var forkSchedule *rpc.EthConfigBlobSchedule

	if cache.clientConfig.Next != nil && !cache.clientConfig.Next.GetActivationTime().After(timestamp) {
		forkSchedule = &cache.clientConfig.Next.BlobSchedule
	} else if cache.clientConfig.Current != nil && !cache.clientConfig.Current.GetActivationTime().After(timestamp) {
		forkSchedule = &cache.clientConfig.Current.BlobSchedule
	} else if cache.clientConfig.Last != nil && !cache.clientConfig.Last.GetActivationTime().After(timestamp) {
		forkSchedule = &cache.clientConfig.Last.BlobSchedule
	}

	return forkSchedule
}

type BlobScheduleEntry struct {
	Timestamp time.Time
	Schedule  rpc.EthConfigBlobSchedule
}

func (cache *ChainState) GetFullBlobSchedule() []BlobScheduleEntry {
	forkSchedules := []BlobScheduleEntry{}

	if cache.config == nil {
		return forkSchedules
	}

	if cache.config.Config.CancunTime != nil && cache.config.Config.BlobScheduleConfig.Cancun != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.CancunTime), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.Cancun.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.Cancun.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.Cancun.UpdateFraction),
			},
		})
	}

	if cache.config.Config.PragueTime != nil && cache.config.Config.BlobScheduleConfig.Prague != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.PragueTime), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.Prague.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.Prague.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.Prague.UpdateFraction),
			},
		})
	}

	if cache.config.Config.OsakaTime != nil && cache.config.Config.BlobScheduleConfig.Osaka != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.OsakaTime), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.Osaka.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.Osaka.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.Osaka.UpdateFraction),
			},
		})
	}

	if cache.config.Config.BPO1Time != nil && cache.config.Config.BlobScheduleConfig.BPO1 != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.BPO1Time), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.BPO1.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.BPO1.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.BPO1.UpdateFraction),
			},
		})
	}

	if cache.config.Config.BPO2Time != nil && cache.config.Config.BlobScheduleConfig.BPO2 != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.BPO2Time), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.BPO2.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.BPO2.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.BPO2.UpdateFraction),
			},
		})
	}

	if cache.config.Config.BPO3Time != nil && cache.config.Config.BlobScheduleConfig.BPO3 != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.BPO3Time), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.BPO3.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.BPO3.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.BPO3.UpdateFraction),
			},
		})
	}

	if cache.config.Config.BPO4Time != nil && cache.config.Config.BlobScheduleConfig.BPO4 != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.BPO4Time), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.BPO4.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.BPO4.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.BPO4.UpdateFraction),
			},
		})
	}

	if cache.config.Config.BPO5Time != nil && cache.config.Config.BlobScheduleConfig.BPO5 != nil {
		forkSchedules = append(forkSchedules, BlobScheduleEntry{
			Timestamp: time.Unix(int64(*cache.config.Config.BPO5Time), 0),
			Schedule: rpc.EthConfigBlobSchedule{
				Max:                   uint64(cache.config.Config.BlobScheduleConfig.BPO5.Max),
				Target:                uint64(cache.config.Config.BlobScheduleConfig.BPO5.Target),
				BaseFeeUpdateFraction: uint64(cache.config.Config.BlobScheduleConfig.BPO5.UpdateFraction),
			},
		})
	}

	sort.Slice(forkSchedules, func(i, j int) bool {
		return forkSchedules[i].Timestamp.Before(forkSchedules[j].Timestamp)
	})

	return forkSchedules
}

const MinBaseFeePerBlobGas = 1 // wei, per EIP-4844

// CalcBaseFeePerBlobGas computes base_fee_per_blob_gas from excess_blob_gas and updateFraction.
// Arguments:
//   - excessBlobGas: from block header
//   - blockTimestamp
//
// Returns base_fee_per_blob_gas as *big.Int
func (cache *ChainState) CalcBaseFeePerBlobGas(excessBlobGas uint64, updateFraction uint64) *big.Int {
	// fake_exponential(minBaseFee, excessBlobGas, updateFraction)
	// base = minBaseFee
	// factor = excessBlobGas / updateFraction
	// remainder = excessBlobGas % updateFraction
	// result = base * (2^factor) * (updateFraction + remainder) / updateFraction

	base := big.NewInt(MinBaseFeePerBlobGas)

	if updateFraction == 0 {
		return big.NewInt(0)
	}

	factor := new(big.Int).Div(new(big.Int).SetUint64(excessBlobGas), new(big.Int).SetUint64(updateFraction))
	remainder := new(big.Int).Mod(new(big.Int).SetUint64(excessBlobGas), new(big.Int).SetUint64(updateFraction))

	// base << factor (multiply by 2^factor)
	base.Lsh(base, uint(factor.Uint64()))

	// multiply by (updateFraction + remainder)
	num := new(big.Int).Add(new(big.Int).SetUint64(updateFraction), remainder)
	base.Mul(base, num)

	// divide by updateFraction
	base.Div(base, new(big.Int).SetUint64(updateFraction))

	return base
}
