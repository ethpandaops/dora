package consensus

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type Block struct {
	Root        phase0.Root
	Slot        phase0.Slot
	lastUsed    time.Time
	headerMutex sync.Mutex
	headerChan  chan bool
	header      *phase0.SignedBeaconBlockHeader
	blockMutex  sync.Mutex
	blockChan   chan bool
	block       *spec.VersionedSignedBeaconBlock
	seenMutex   sync.RWMutex
	seenMap     map[uint16]*Client
}

func (block *Block) GetSeenBy() []*Client {
	block.seenMutex.RLock()
	defer block.seenMutex.RUnlock()

	clients := []*Client{}

	for _, client := range block.seenMap {
		clients = append(clients, client)
	}

	sort.Slice(clients, func(a, b int) bool {
		return clients[a].clientIdx < clients[b].clientIdx
	})

	return clients
}

func (block *Block) SetSeenBy(client *Client) {
	block.seenMutex.Lock()
	defer block.seenMutex.Unlock()
	block.seenMap[client.clientIdx] = client
}

func (block *Block) GetHeader() *phase0.SignedBeaconBlockHeader {
	return block.header
}

func (block *Block) AwaitHeader(ctx context.Context, timeout time.Duration) *phase0.SignedBeaconBlockHeader {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-block.headerChan:
	case <-time.After(timeout):
	case <-ctx.Done():
	}

	return block.header
}

func (block *Block) GetBlock() *spec.VersionedSignedBeaconBlock {
	return block.block
}

func (block *Block) AwaitBlock(ctx context.Context, timeout time.Duration) *spec.VersionedSignedBeaconBlock {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-block.blockChan:
	case <-time.After(timeout):
	case <-ctx.Done():
	}

	return block.block
}

func (block *Block) GetParentRoot() *phase0.Root {
	if block.header == nil {
		return nil
	}

	return &block.header.Message.ParentRoot
}

func (block *Block) SetHeader(header *phase0.SignedBeaconBlockHeader) {
	block.header = header
}

func (block *Block) EnsureHeader(loadHeader func() (*phase0.SignedBeaconBlockHeader, error)) error {
	if block.header != nil {
		return nil
	}

	block.headerMutex.Lock()
	defer block.headerMutex.Unlock()

	if block.header != nil {
		return nil
	}

	header, err := loadHeader()
	if err != nil {
		return err
	}

	block.header = header
	close(block.headerChan)

	return nil
}

func (block *Block) EnsureBlock(loadBlock func() (*spec.VersionedSignedBeaconBlock, error)) (bool, error) {
	if block.block != nil {
		return false, nil
	}

	block.blockMutex.Lock()
	defer block.blockMutex.Unlock()

	if block.block != nil {
		return false, nil
	}

	blockBody, err := loadBlock()
	if err != nil {
		return false, err
	}

	block.block = blockBody
	close(block.blockChan)

	return true, nil
}
