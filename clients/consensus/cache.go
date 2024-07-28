package consensus

import (
	"bytes"
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/ethwallclock"
	"github.com/mashingan/smapping"
	"github.com/sirupsen/logrus"
)

type cache struct {
	cacheDuration time.Duration

	specMutex sync.RWMutex
	specs     *ChainSpec

	genesisMutex sync.Mutex
	genesis      *v1.Genesis

	wallclockMutex sync.Mutex
	wallclock      *ethwallclock.EthereumBeaconChain

	finalizedMutex sync.RWMutex
	finalizedEpoch phase0.Epoch
	finalizedRoot  phase0.Root

	blockMutex   sync.RWMutex
	blockRootMap map[phase0.Root]*Block

	blockDispatcher          Dispatcher[*Block]
	checkpointDispatcher     Dispatcher[*FinalizedCheckpoint]
	wallclockEpochDispatcher Dispatcher[*ethwallclock.Epoch]
	wallclockSlotDispatcher  Dispatcher[*ethwallclock.Slot]
}

func newCache(ctx context.Context, logger logrus.FieldLogger) (*cache, error) {
	cache := cache{
		cacheDuration: 5 * time.Minute,
		blockRootMap:  make(map[phase0.Root]*Block),
	}

	go cache.startCacheCleanupRoutine(ctx, logger)

	return &cache, nil
}

func (cache *cache) startCacheCleanupRoutine(ctx context.Context, logger logrus.FieldLogger) {
	defer func() {
		if err := recover(); err != nil && ctx.Err() == nil {
			logger.WithError(err.(error)).Errorf("uncaught panic in BlockCache.runCacheCleanup subroutine: %v, stack: %v", err, string(debug.Stack()))

			go cache.startCacheCleanupRoutine(ctx, logger)
		}
	}()

	cache.runCacheCleanup(ctx)
}

func (cache *cache) notifyBlockReady(block *Block) {
	cache.blockDispatcher.Fire(block)
}

func (cache *cache) setGenesis(genesis *v1.Genesis) error {
	cache.genesisMutex.Lock()
	defer cache.genesisMutex.Unlock()

	if cache.genesis != nil {
		if cache.genesis.GenesisTime != genesis.GenesisTime {
			return fmt.Errorf("genesis mismatch: GenesisTime")
		}

		if !bytes.Equal(cache.genesis.GenesisValidatorsRoot[:], genesis.GenesisValidatorsRoot[:]) {
			return fmt.Errorf("genesis mismatch: GenesisValidatorsRoot")
		}
	} else {
		cache.genesis = genesis
	}

	return nil
}

func (cache *cache) setClientSpecs(specValues map[string]interface{}) error {
	cache.specMutex.Lock()
	defer cache.specMutex.Unlock()

	specs := ChainSpec{}

	err := smapping.FillStructByTags(&specs, specValues, "yaml")
	if err != nil {
		return err
	}

	if cache.specs != nil {
		mismatches := cache.specs.CheckMismatch(&specs)
		if len(mismatches) > 0 {
			return fmt.Errorf("spec mismatch: %v", strings.Join(mismatches, ", "))
		}
	}

	cache.specs = &specs

	return nil
}

func (cache *cache) getSpecs() *ChainSpec {
	cache.specMutex.RLock()
	defer cache.specMutex.RUnlock()

	return cache.specs
}

func (cache *cache) initWallclock() {
	cache.wallclockMutex.Lock()
	defer cache.wallclockMutex.Unlock()

	if cache.wallclock != nil {
		return
	}

	specs := cache.getSpecs()
	if specs == nil || cache.genesis == nil {
		return
	}

	cache.wallclock = ethwallclock.NewEthereumBeaconChain(cache.genesis.GenesisTime, specs.SecondsPerSlot, specs.SlotsPerEpoch)
	cache.wallclock.OnEpochChanged(func(current ethwallclock.Epoch) {
		cache.wallclockEpochDispatcher.Fire(&current)
	})
	cache.wallclock.OnSlotChanged(func(current ethwallclock.Slot) {
		cache.wallclockSlotDispatcher.Fire(&current)
	})
}

func (cache *cache) setFinalizedCheckpoint(finalizedEpoch phase0.Epoch, finalizedRoot phase0.Root) {
	cache.finalizedMutex.Lock()
	if finalizedEpoch <= cache.finalizedEpoch {
		cache.finalizedMutex.Unlock()
		return
	}

	cache.finalizedEpoch = finalizedEpoch
	cache.finalizedRoot = finalizedRoot
	cache.finalizedMutex.Unlock()

	cache.checkpointDispatcher.Fire(&FinalizedCheckpoint{
		Epoch: finalizedEpoch,
		Root:  finalizedRoot,
	})
}

func (cache *cache) getFinalizedCheckpoint() (phase0.Epoch, phase0.Root) {
	cache.finalizedMutex.RLock()
	defer cache.finalizedMutex.RUnlock()

	return cache.finalizedEpoch, cache.finalizedRoot
}

func (cache *cache) getOrCreateBlock(root phase0.Root, slot phase0.Slot) (*Block, bool) {
	cache.blockMutex.Lock()
	defer cache.blockMutex.Unlock()

	if cache.blockRootMap[root] != nil {
		cache.blockRootMap[root].lastUsed = time.Now()
		return cache.blockRootMap[root], false
	}

	cacheBlock := &Block{
		Root:       root,
		Slot:       slot,
		lastUsed:   time.Now(),
		seenMap:    make(map[uint16]*Client),
		headerChan: make(chan bool),
		blockChan:  make(chan bool),
	}
	cache.blockRootMap[root] = cacheBlock

	return cacheBlock, true
}

func (cache *cache) runCacheCleanup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		cache.cleanupBlockCache()
	}
}

func (cache *cache) cleanupBlockCache() {
	cache.blockMutex.Lock()
	defer cache.blockMutex.Unlock()

	for _, block := range cache.blockRootMap {
		if time.Since(block.lastUsed) > cache.cacheDuration {
			continue
		}

		delete(cache.blockRootMap, block.Root)
	}
}
