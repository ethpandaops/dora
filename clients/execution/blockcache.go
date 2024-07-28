package execution

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution/rpc"
)

type BlockCache struct {
	followDistance  uint64
	specMutex       sync.RWMutex
	specs           *rpc.ChainSpec
	cacheMutex      sync.RWMutex
	numberMap       map[uint64][]*Block
	hashMap         map[common.Hash]*Block
	maxSlotIdx      int64
	blockDispatcher Dispatcher[*Block]
}

func NewBlockCache(ctx context.Context, logger logrus.FieldLogger, followDistance uint64) (*BlockCache, error) {
	if followDistance == 0 {
		return nil, fmt.Errorf("cannot initialize block cache without follow distance")
	}

	cache := BlockCache{
		followDistance: followDistance,
		numberMap:      make(map[uint64][]*Block),
		hashMap:        make(map[common.Hash]*Block),
	}

	go func() {
		defer func() {
			if err := recover(); err != nil {
				logger.WithError(err.(error)).Errorf("uncaught panic in BlockCache.runCacheCleanup subroutine: %v, stack: %v", err, string(debug.Stack()))
			}
		}()
		cache.runCacheCleanup(ctx)
	}()

	return &cache, nil
}

func (cache *BlockCache) SubscribeBlockEvent(capacity int) *Subscription[*Block] {
	return cache.blockDispatcher.Subscribe(capacity)
}

func (cache *BlockCache) UnsubscribeBlockEvent(subscription *Subscription[*Block]) {
	cache.blockDispatcher.Unsubscribe(subscription)
}

func (cache *BlockCache) notifyBlockReady(block *Block) {
	cache.blockDispatcher.Fire(block)
}

func (cache *BlockCache) SetMinFollowDistance(followDistance uint64) {
	if followDistance > cache.followDistance {
		cache.followDistance = followDistance
	}
}

func (cache *BlockCache) SetClientSpecs(specs *rpc.ChainSpec) error {
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

func (cache *BlockCache) GetSpecs() *rpc.ChainSpec {
	cache.specMutex.RLock()
	defer cache.specMutex.RUnlock()

	return cache.specs
}

func (cache *BlockCache) GetChainID() *big.Int {
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

func (cache *BlockCache) AddBlock(hash common.Hash, number uint64) (*Block, bool) {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	if cache.hashMap[hash] != nil {
		return cache.hashMap[hash], false
	}

	if int64(number) < cache.maxSlotIdx-int64(cache.followDistance) {
		return nil, false
	}

	cacheBlock := &Block{
		Hash:      hash,
		Number:    number,
		seenMap:   make(map[uint16]*Client),
		blockChan: make(chan bool),
	}
	cache.hashMap[hash] = cacheBlock

	if cache.numberMap[number] == nil {
		cache.numberMap[number] = []*Block{cacheBlock}
	} else {
		cache.numberMap[number] = append(cache.numberMap[number], cacheBlock)
	}

	if int64(number) > cache.maxSlotIdx {
		cache.maxSlotIdx = int64(number)
	}

	return cacheBlock, true
}

func (cache *BlockCache) GetCachedBlockByRoot(hash common.Hash) *Block {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	return cache.hashMap[hash]
}

func (cache *BlockCache) GetCachedBlocks() []*Block {
	cache.cacheMutex.RLock()
	defer cache.cacheMutex.RUnlock()

	blocks := []*Block{}
	slots := []uint64{}

	for slot := range cache.numberMap {
		slots = append(slots, slot)
	}

	sort.Slice(slots, func(a, b int) bool {
		return slots[a] > slots[b]
	})

	for _, slot := range slots {
		blocks = append(blocks, cache.numberMap[slot]...)
	}

	return blocks
}

func (cache *BlockCache) runCacheCleanup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		cache.cleanupCache()
	}
}

func (cache *BlockCache) cleanupCache() {
	cache.cacheMutex.Lock()
	defer cache.cacheMutex.Unlock()

	minSlot := cache.maxSlotIdx - int64(cache.followDistance)
	if minSlot <= 0 {
		return
	}

	for slot, blocks := range cache.numberMap {
		if slot >= uint64(minSlot) {
			continue
		}

		for _, block := range blocks {
			delete(cache.hashMap, block.Hash)
		}

		delete(cache.numberMap, slot)
	}
}

func (cache *BlockCache) IsCanonicalBlock(blockRoot, headRoot common.Hash) bool {
	res, _ := cache.GetBlockDistance(blockRoot, headRoot)
	return res
}

func (cache *BlockCache) GetBlockDistance(blockRoot, headRoot common.Hash) (linked bool, distance uint64) {
	if bytes.Equal(headRoot[:], blockRoot[:]) {
		return true, 0
	}

	block := cache.GetCachedBlockByRoot(blockRoot)
	if block == nil {
		return false, 0
	}

	blockSlot := block.Number
	headBlock := cache.GetCachedBlockByRoot(headRoot)
	dist := uint64(0)

	for headBlock != nil {
		if headBlock.Number < blockSlot {
			return false, 0
		}

		parentRoot := headBlock.GetParentHash()
		if parentRoot == nil {
			return false, 0
		}

		dist++
		if bytes.Equal(parentRoot[:], blockRoot[:]) {
			return true, dist
		}

		headBlock = cache.GetCachedBlockByRoot(*parentRoot)
	}

	return false, 0
}
