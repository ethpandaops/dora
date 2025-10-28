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

// CalcBaseFeePerBlobGas computes base_fee_per_blob_gas from excess_blob_gas and updateFraction
// according to EIP-4844 fake_exponential(MIN_BASE_FEE_PER_BLOB_GAS, excess_blob_gas, updateFraction).
// Returns base_fee_per_blob_gas as *big.Int (>= MIN_BASE_FEE_PER_BLOB_GAS; 0 if updateFraction==0).
func (cache *ChainState) CalcBaseFeePerBlobGas(excessBlobGas uint64, updateFraction uint64) *big.Int {
	if updateFraction == 0 {
		return big.NewInt(0)
	}

	minBase := big.NewInt(MinBaseFeePerBlobGas)           // EIP-4844: MIN_BASE_FEE_PER_BLOB_GAS = 1
	numerator := new(big.Int).SetUint64(excessBlobGas)    // n
	denominator := new(big.Int).SetUint64(updateFraction) // d

	i := big.NewInt(1)
	output := new(big.Int) // 0

	// numerator_accum = minBase * denominator
	numeratorAccum := new(big.Int).Mul(minBase, denominator)

	tmp := new(big.Int)
	for numeratorAccum.Sign() > 0 {
		// output += numerator_accum
		output.Add(output, numeratorAccum)

		// numerator_accum = (numerator_accum * numerator) // (denominator * i)
		tmp.Mul(denominator, i) // tmp = d * i
		if tmp.Sign() == 0 {    // defensive (shouldn't happen since updateFraction != 0)
			break
		}
		numeratorAccum.Mul(numeratorAccum, numerator)
		numeratorAccum.Div(numeratorAccum, tmp)

		i.Add(i, big.NewInt(1))
	}

	// return output // denominator
	return output.Div(output, denominator)
}

// calculateEIP7918BlobBaseFee implements the EIP-7918 reserve price mechanism
// It ensures blob base fee doesn't fall below a threshold based on execution costs
func (cache *ChainState) CalculateEIP7918BlobBaseFee(baseFeePerGas uint64, blobBaseFee uint64) uint64 {
	// EIP-7918 constants
	const (
		BLOB_BASE_COST = 8192   // 2^13
		GAS_PER_BLOB   = 131072 // blob gas limit per blob (128KB)
	)

	// Calculate the reserve price threshold
	// If BLOB_BASE_COST * base_fee_per_gas > GAS_PER_BLOB * base_fee_per_blob_gas
	// then the blob base fee should be adjusted
	reservePriceThreshold := (BLOB_BASE_COST * baseFeePerGas) / GAS_PER_BLOB

	// Return the higher of the two values
	if reservePriceThreshold > blobBaseFee {
		return reservePriceThreshold
	}
	return blobBaseFee
}
