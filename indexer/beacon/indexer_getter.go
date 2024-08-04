package beacon

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
)

func (indexer *Indexer) GetAllClients() []*Client {
	clients := make([]*Client, len(indexer.clients))
	copy(clients, indexer.clients)
	return clients
}

func (indexer *Indexer) GetReadyClientsByCheckpoint(finalizedRoot phase0.Root, preferArchive bool) []*Client {
	clients := make([]*Client, 0)

	for _, client := range indexer.clients {
		if client.client.GetStatus() != consensus.ClientStatusOnline {
			continue
		}

		_, root, _, _ := client.client.GetFinalityCheckpoint()
		if !bytes.Equal(root[:], finalizedRoot[:]) && !bytes.Equal(root[:], consensus.NullRoot[:]) {
			continue
		}

		clients = append(clients, client)
	}

	sort.Slice(clients, func(i, j int) bool {
		if preferArchive && clients[i].archive != clients[j].archive {
			return clients[i].archive
		}

		if clients[i].priority != clients[j].priority {
			return clients[i].priority > clients[j].priority
		}

		return rand.IntN(2) == 0
	})

	return clients
}

func (indexer *Indexer) GetReadyClientsByBlockRoot(blockRoot phase0.Root, preferArchive bool) []*Client {
	clients := make([]*Client, 0)

	for _, client := range indexer.clients {
		if client.client.GetStatus() != consensus.ClientStatusOnline {
			continue
		}

		_, root := client.client.GetLastHead()
		if indexer.blockCache.isCanonicalBlock(blockRoot, root) {
			clients = append(clients, client)
		}
	}

	sort.Slice(clients, func(i, j int) bool {
		if preferArchive && clients[i].archive != clients[j].archive {
			return clients[i].archive
		}

		if clients[i].priority != clients[j].priority {
			return clients[i].priority > clients[j].priority
		}

		return rand.IntN(2) == 0
	})

	return clients
}

func (indexer *Indexer) GetReadyClientByBlockRoot(blockRoot phase0.Root, preferArchive bool) *Client {
	clients := indexer.GetReadyClientsByBlockRoot(blockRoot, preferArchive)
	if len(clients) > 0 {
		return clients[0]
	}

	return nil
}

func (indexer *Indexer) GetReadyClients(preferArchive bool) []*Client {
	_, finalizedRoot := indexer.consensusPool.GetChainState().GetFinalizedCheckpoint()
	clients := indexer.GetReadyClientsByCheckpoint(finalizedRoot, preferArchive)
	if len(clients) == 0 {
		clients = indexer.GetReadyClientsByCheckpoint(consensus.NullRoot, preferArchive)
	}
	return clients
}

func (indexer *Indexer) GetBlockCacheState() (finalizedEpoch phase0.Epoch, prunedEpoch phase0.Epoch) {
	return indexer.lastFinalizedEpoch, indexer.lastPrunedEpoch
}

func (indexer *Indexer) GetForkHeads() []*ForkHead {
	return indexer.forkCache.getForkHeads()
}

func (indexer *Indexer) GetBlockByRoot(blockRoot phase0.Root) *Block {
	return indexer.blockCache.getBlockByRoot(blockRoot)
}

func (indexer *Indexer) GetBlocksBySlot(slot phase0.Slot) []*Block {
	return indexer.blockCache.getBlocksBySlot(slot)
}

func (indexer *Indexer) GetBlockByParentRoot(blockRoot phase0.Root) []*Block {
	return indexer.blockCache.getBlocksByParentRoot(blockRoot)
}

func (indexer *Indexer) GetBlockByStateRoot(stateRoot phase0.Root) *Block {
	return indexer.blockCache.getBlockByStateRoot(stateRoot)
}

func (indexer *Indexer) GetBlocksByExecutionBlockHash(blockHash phase0.Hash32) []*Block {
	return indexer.blockCache.getBlocksByExecutionBlockHash(blockHash)
}

func (indexer *Indexer) GetBlocksByExecutionBlockNumber(blockNumber uint64) []*Block {
	return indexer.blockCache.getBlocksByExecutionBlockNumber(blockNumber)
}

func (indexer *Indexer) GetBlockDistance(baseRoot phase0.Root, headRoot phase0.Root) (bool, uint64) {
	return indexer.blockCache.getCanonicalDistance(baseRoot, headRoot, 0)
}

func (indexer *Indexer) GetOrphanedBlockByRoot(blockRoot phase0.Root) (*Block, error) {
	orphanedBlock := db.GetOrphanedBlock(blockRoot[:])
	if orphanedBlock == nil {
		return nil, nil
	}

	if orphanedBlock.HeaderVer != 1 {
		return nil, fmt.Errorf("failed unmarshal orphaned block header [%x] from db: unsupported header version", orphanedBlock.Root)
	}

	header := &phase0.SignedBeaconBlockHeader{}
	err := header.UnmarshalSSZ(orphanedBlock.HeaderSSZ)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshal orphaned block header [%x] from db: %v", orphanedBlock.Root, err)
	}

	blockBody, err := unmarshalVersionedSignedBeaconBlockSSZ(indexer.dynSsz, orphanedBlock.BlockVer, orphanedBlock.BlockSSZ)
	if err != nil {
		return nil, fmt.Errorf("could not restore orphaned block body %v [%x] from db: %v", header.Message.Slot, orphanedBlock.Root, err)
	}

	block := newBlock(indexer.dynSsz, blockRoot, header.Message.Slot)
	block.SetHeader(header)
	block.SetBlock(blockBody)

	return block, nil
}

func (indexer *Indexer) GetEpochStats(epoch phase0.Epoch, overrideForkId *ForkKey) *EpochStats {
	epochStats := indexer.epochCache.getEpochStatsByEpoch(epoch)
	if len(epochStats) == 0 {
		return nil
	}

	if len(epochStats) == 1 {
		return epochStats[0]
	}

	canonicalHead := indexer.GetCanonicalHead(overrideForkId)

	var bestEpochStats *EpochStats
	var bestDistance uint64

	if canonicalHead != nil {
		for _, stats := range epochStats {
			if !stats.ready {
				continue
			}
			if isInChain, distance := indexer.blockCache.getCanonicalDistance(stats.dependentRoot, canonicalHead.Root, 0); isInChain {
				if bestEpochStats == nil || distance < bestDistance {
					bestEpochStats = stats
					bestDistance = distance
				}
			}
		}

		if bestEpochStats == nil {
			// retry with non ready states
			for _, stats := range epochStats {
				if stats.ready {
					continue
				}
				if isInChain, distance := indexer.blockCache.getCanonicalDistance(stats.dependentRoot, canonicalHead.Root, 0); isInChain {
					if bestEpochStats == nil || distance < bestDistance {
						bestEpochStats = stats
						bestDistance = distance
					}
				}
			}
		}
	}

	if bestEpochStats == nil {
		bestEpochStats = epochStats[0]
	}

	return bestEpochStats
}
