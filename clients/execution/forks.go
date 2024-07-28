package execution

import (
	"bytes"
	"sort"

	"github.com/ethereum/go-ethereum/common"
)

type HeadFork struct {
	Number       uint64
	Hash         common.Hash
	ReadyClients []*Client
	AllClients   []*Client
}

func (pool *Pool) resetHeadForkCache() {
	pool.forkCacheMutex.Lock()
	defer pool.forkCacheMutex.Unlock()
	pool.forkCache = map[int64][]*HeadFork{}
}

func (pool *Pool) GetCanonicalFork(forkDistance int64) *HeadFork {
	forks := pool.GetHeadForks(forkDistance)
	if len(forks) == 0 {
		return nil
	}

	return forks[0]
}

func (pool *Pool) GetHeadForks(forkDistance int64) []*HeadFork {
	if forkDistance < 0 {
		forkDistance = int64(pool.config.ForkDistance)
	}

	pool.forkCacheMutex.Lock()
	defer pool.forkCacheMutex.Unlock()

	if pool.forkCache[forkDistance] != nil {
		return pool.forkCache[forkDistance]
	}

	headForks := []*HeadFork{}

	for _, client := range pool.clients {
		var matchingFork *HeadFork

		cHeadSlot, cHeadRoot := client.GetLastHead()

		for _, fork := range headForks {
			if bytes.Equal(fork.Hash[:], cHeadRoot[:]) || pool.blockCache.IsCanonicalBlock(cHeadRoot, fork.Hash) {
				matchingFork = fork
				break
			}

			if pool.blockCache.IsCanonicalBlock(fork.Hash, cHeadRoot) {
				fork.Hash = cHeadRoot
				fork.Number = cHeadSlot
				matchingFork = fork

				break
			}
		}

		if matchingFork == nil {
			matchingFork = &HeadFork{
				Hash:       cHeadRoot,
				Number:     cHeadSlot,
				AllClients: []*Client{client},
			}
			headForks = append(headForks, matchingFork)
		} else {
			matchingFork.AllClients = append(matchingFork.AllClients, client)
		}
	}

	for _, fork := range headForks {
		fork.ReadyClients = make([]*Client, 0)
		for _, client := range fork.AllClients {
			if client.GetStatus() != ClientStatusOnline {
				continue
			}

			var headDistance uint64

			_, cHeadRoot := client.GetLastHead()
			if !bytes.Equal(fork.Hash[:], cHeadRoot[:]) {
				_, headDistance = pool.blockCache.GetBlockDistance(cHeadRoot, fork.Hash)
			}

			if headDistance <= uint64(forkDistance) {
				fork.ReadyClients = append(fork.ReadyClients, client)
			}
		}
	}

	// sort by relevance (client count)
	sort.Slice(headForks, func(a, b int) bool {
		countA := len(headForks[a].ReadyClients)
		countB := len(headForks[b].ReadyClients)

		return countA > countB
	})

	pool.forkCache[forkDistance] = headForks

	return headForks
}

func (fork *HeadFork) IsClientReady(client *Client) bool {
	if fork == nil {
		return false
	}

	for _, cli := range fork.ReadyClients {
		if cli.clientIdx == client.clientIdx {
			return true
		}
	}

	return false
}
