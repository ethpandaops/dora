package execution

import (
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethpandaops/dora/clients/execution/rpc"
)

type ChainState struct {
	specMutex sync.RWMutex
	specs     *rpc.ChainSpec
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
