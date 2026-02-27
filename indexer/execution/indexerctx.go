package execution

import (
	"context"
	"math/rand/v2"
	"slices"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/sirupsen/logrus"
)

// IndexerCtx is the context for the execution indexer
type IndexerCtx struct {
	Ctx              context.Context
	Logger           logrus.FieldLogger
	BeaconIndexer    *beacon.Indexer
	ExecutionPool    *execution.Pool
	ConsensusPool    *consensus.Pool
	ChainState       *consensus.ChainState
	executionClients map[*execution.Client]*indexerElClientInfo
}

// indexerElClientInfo holds information about a client and its priority
type indexerElClientInfo struct {
	priority int
	archive  bool
}

// NewIndexerCtx creates a new IndexerCtx
func NewIndexerCtx(ctx context.Context, logger logrus.FieldLogger, executionPool *execution.Pool, consensusPool *consensus.Pool, beaconIndexer *beacon.Indexer) *IndexerCtx {
	return &IndexerCtx{
		Ctx:              ctx,
		Logger:           logger,
		ExecutionPool:    executionPool,
		ConsensusPool:    consensusPool,
		BeaconIndexer:    beaconIndexer,
		ChainState:       consensusPool.GetChainState(),
		executionClients: map[*execution.Client]*indexerElClientInfo{},
	}
}

// AddClientInfo adds client info to the indexer context
func (ictx *IndexerCtx) AddClientInfo(client *execution.Client, priority int, archive bool) {
	ictx.executionClients[client] = &indexerElClientInfo{
		priority: priority,
		archive:  archive,
	}
}

// getFinalizedClients returns a list of clients that have reached the finalized el block
func (ictx *IndexerCtx) GetFinalizedClients(clientType execution.ClientType) []*execution.Client {
	_, finalizedRoot := ictx.ConsensusPool.GetChainState().GetJustifiedCheckpoint()

	finalizedClients := make([]*execution.Client, 0)
	for _, client := range ictx.ExecutionPool.GetReadyEndpoints(clientType) {
		_, blockHash := client.GetLastHead()
		for _, beaconBlock := range ictx.BeaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash)) {
			isInChain, _ := ictx.BeaconIndexer.GetBlockDistance(finalizedRoot, beaconBlock.Root)
			if isInChain {
				finalizedClients = append(finalizedClients, client)
				break
			}
		}
	}

	return finalizedClients
}

// getFinalizedClients returns a list of clients that have reached the finalized el block
func (ictx *IndexerCtx) GetClientsOnFork(forkId beacon.ForkKey, clientType execution.ClientType) []*execution.Client {
	clients := make([]*execution.Client, 0)
	for _, client := range ictx.ExecutionPool.GetReadyEndpoints(clientType) {
		_, blockHash := client.GetLastHead()
		for _, block := range ictx.BeaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash)) {

			parentForkIds := ictx.BeaconIndexer.GetParentForkIds(block.GetForkId())
			if !slices.Contains(parentForkIds, forkId) {
				continue
			}
			clients = append(clients, client)
		}
	}

	return clients
}

// SortClients sorts clients by priority, but randomizes the order for equal priority.
// Returns true if clientA should come before clientB.
func (ictx *IndexerCtx) SortClients(clientA *execution.Client, clientB *execution.Client, preferArchive bool) bool {
	clientAInfo := ictx.executionClients[clientA]
	clientBInfo := ictx.executionClients[clientB]

	if preferArchive && clientAInfo.archive != clientBInfo.archive {
		return clientAInfo.archive
	}

	if clientAInfo.priority != clientBInfo.priority {
		return clientAInfo.priority > clientBInfo.priority
	}

	return rand.IntN(2) == 0
}

// forkWithClients holds information about a fork and the clients following it
type ForkWithClients struct {
	Canonical bool
	ForkId    beacon.ForkKey
	ForkHead  *beacon.ForkHead
	Clients   []*execution.Client
}

// getForksWithClients returns a list of forks with their clients
// the list is sorted by the canonical head and the number of clients
func (ictx *IndexerCtx) GetForksWithClients(clientType execution.ClientType) []*ForkWithClients {
	forksWithClients := make([]*ForkWithClients, 0)
	forkHeadMap := map[beacon.ForkKey]*beacon.ForkHead{}
	for _, forkHead := range ictx.BeaconIndexer.GetForkHeads() {
		forkHeadMap[forkHead.ForkId] = forkHead
	}

	for _, client := range ictx.ExecutionPool.GetReadyEndpoints(clientType) {
		_, blockHash := client.GetLastHead()
		for _, block := range ictx.BeaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash)) {

			var matchingForkWithClients *ForkWithClients
			for _, forkWithClients := range forksWithClients {
				if forkWithClients.ForkId == block.GetForkId() {
					matchingForkWithClients = forkWithClients
					break
				}
			}

			if matchingForkWithClients == nil {
				matchingForkWithClients = &ForkWithClients{
					ForkId:   block.GetForkId(),
					ForkHead: forkHeadMap[block.GetForkId()],
				}
				forksWithClients = append(forksWithClients, matchingForkWithClients)
			}

			matchingForkWithClients.Clients = append(matchingForkWithClients.Clients, client)
		}
	}

	canonicalHead := ictx.BeaconIndexer.GetCanonicalHead(nil)

	for _, forkWithClients := range forksWithClients {
		forkWithClients.Canonical = canonicalHead != nil && canonicalHead.GetForkId() == forkWithClients.ForkId

		sort.Slice(forkWithClients.Clients, func(i, j int) bool {
			return ictx.SortClients(forkWithClients.Clients[i], forkWithClients.Clients[j], true)
		})
	}

	sort.Slice(forksWithClients, func(i, j int) bool {
		cliA := forksWithClients[i]
		cliB := forksWithClients[j]
		if cliA.Canonical != cliB.Canonical {
			return cliA.Canonical
		}

		return len(cliA.Clients) > len(cliB.Clients)
	})

	return forksWithClients
}

// GetSystemContractAddress returns the address of a system contract from the first available client's config
func (ictx *IndexerCtx) GetSystemContractAddress(contractType string) common.Address {
	return ictx.ExecutionPool.GetChainState().GetSystemContractAddress(contractType)
}
