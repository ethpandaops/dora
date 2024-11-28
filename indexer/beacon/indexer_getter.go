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

// GetAllClients returns a slice of all clients in the indexer.
func (indexer *Indexer) GetAllClients() []*Client {
	clients := make([]*Client, len(indexer.clients))
	copy(clients, indexer.clients)
	return clients
}

// GetReadyClientsByCheckpoint returns a slice of clients that are ready for processing based on the finalized root and preference for archive clients.
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

// GetReadyClientsByBlockRoot returns a slice of clients that are ready for requests for the chain including the block root and preference for archive clients.
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

// GetReadyClientByBlockRoot returns a single client that is ready for requests for the chain including the block root and preference for archive clients.
func (indexer *Indexer) GetReadyClientByBlockRoot(blockRoot phase0.Root, preferArchive bool) *Client {
	clients := indexer.GetReadyClientsByBlockRoot(blockRoot, preferArchive)
	if len(clients) > 0 {
		return clients[0]
	}

	return nil
}

// GetReadyClients returns a slice of clients that are on the finalized chain and preference for archive clients.
func (indexer *Indexer) GetReadyClients(preferArchive bool) []*Client {
	_, finalizedRoot := indexer.consensusPool.GetChainState().GetFinalizedCheckpoint()
	clients := indexer.GetReadyClientsByCheckpoint(finalizedRoot, preferArchive)
	if len(clients) == 0 {
		clients = indexer.GetReadyClientsByCheckpoint(consensus.NullRoot, preferArchive)
	}
	return clients
}

// GetReadyClient returns a single client that is on the finalized chain and preference for archive clients.
func (indexer *Indexer) GetReadyClient(preferArchive bool) *Client {
	clients := indexer.GetReadyClients(preferArchive)
	if len(clients) > 0 {
		return clients[0]
	}

	return nil
}

// GetBlockCacheState returns the state of the block cache, including the last finalized epoch and the last pruned epoch.
// this represents the internal cache state and might be behind the actual finalization checkpoint.
func (indexer *Indexer) GetBlockCacheState() (finalizedEpoch phase0.Epoch, prunedEpoch phase0.Epoch) {
	return indexer.lastFinalizedEpoch, indexer.lastPrunedEpoch
}

// GetSynchronizerState returns the state of the synchronizer, including whether it is running and the current epoch.
func (indexer *Indexer) GetSynchronizerState() (running bool, syncHead phase0.Epoch) {
	if indexer.synchronizer == nil {
		return false, 0
	}

	return indexer.synchronizer.running, indexer.synchronizer.currentEpoch
}

// GetForkHeads returns a slice of fork heads in the indexer.
func (indexer *Indexer) GetForkHeads() []*ForkHead {
	return indexer.forkCache.getForkHeads()
}

// GetBlockByRoot returns the block with the given block root.
func (indexer *Indexer) GetBlockByRoot(blockRoot phase0.Root) *Block {
	return indexer.blockCache.getBlockByRoot(blockRoot)
}

// GetBlocksBySlot returns a slice of blocks with the given slot.
func (indexer *Indexer) GetBlocksBySlot(slot phase0.Slot) []*Block {
	return indexer.blockCache.getBlocksBySlot(slot)
}

// GetBlockByParentRoot returns a slice of blocks with the given parent root.
func (indexer *Indexer) GetBlockByParentRoot(blockRoot phase0.Root) []*Block {
	return indexer.blockCache.getBlocksByParentRoot(blockRoot)
}

// GetBlockByStateRoot returns the block with the given state root.
func (indexer *Indexer) GetBlockByStateRoot(stateRoot phase0.Root) *Block {
	return indexer.blockCache.getBlockByStateRoot(stateRoot)
}

// GetBlocksByExecutionBlockHash returns a slice of blocks with the given execution block hash.
func (indexer *Indexer) GetBlocksByExecutionBlockHash(blockHash phase0.Hash32) []*Block {
	return indexer.blockCache.getBlocksByExecutionBlockHash(blockHash)
}

// GetBlocksByExecutionBlockNumber returns a slice of blocks with the given execution block number.
func (indexer *Indexer) GetBlocksByExecutionBlockNumber(blockNumber uint64) []*Block {
	return indexer.blockCache.getBlocksByExecutionBlockNumber(blockNumber)
}

// GetBlockDistance returns whether the base root is in the canonical chain defined by the head root and the distance between both blocks.
func (indexer *Indexer) GetBlockDistance(baseRoot phase0.Root, headRoot phase0.Root) (bool, uint64) {
	return indexer.blockCache.getCanonicalDistance(baseRoot, headRoot, 0)
}

// GetOrphanedBlockByRoot returns the orphaned block with the given block root.
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

// GetEpochStats returns the epoch stats for the given epoch and optional fork ID override.
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

// GetParentForkIds returns the parent fork ids of the given fork.
func (indexer *Indexer) GetParentForkIds(forkId ForkKey) []ForkKey {
	return indexer.forkCache.getParentForkIds(forkId)
}

// GetValidatorSet returns the most recent basic validator set, excluding balances and validator status.
// If an overrideForkId is provided, the validator set for the fork is returned.
func (indexer *Indexer) GetValidatorSet(overrideForkId *ForkKey) []*phase0.Validator {
	return indexer.validatorCache.getValidatorSet(overrideForkId)
}

// GetValidatorIndexByPubkey returns the validator index for a given pubkey.
func (indexer *Indexer) GetValidatorIndexByPubkey(pubkey phase0.BLSPubKey) (phase0.ValidatorIndex, bool) {
	return indexer.validatorCache.getValidatorIndexByPubkey(pubkey)
}

// GetValidatorByIndex returns the validator by index for a given forkId.
func (indexer *Indexer) GetValidatorByIndex(index phase0.ValidatorIndex, overrideForkId *ForkKey) *phase0.Validator {
	return indexer.validatorCache.getValidatorByIndex(index, overrideForkId)
}
