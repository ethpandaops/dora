package execution

import (
	"math/rand/v2"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/sirupsen/logrus"
)

type IndexerCtx struct {
	logger           logrus.FieldLogger
	beaconIndexer    *beacon.Indexer
	executionPool    *execution.Pool
	consensusPool    *consensus.Pool
	chainState       *consensus.ChainState
	executionClients map[*execution.Client]*indexerElClientInfo
}

type indexerElClientInfo struct {
	priority int
	archive  bool
}

// NewIndexerCtx creates a new IndexerCtx
func NewIndexerCtx(logger logrus.FieldLogger, executionPool *execution.Pool, consensusPool *consensus.Pool, beaconIndexer *beacon.Indexer) *IndexerCtx {
	return &IndexerCtx{
		logger:           logger,
		executionPool:    executionPool,
		consensusPool:    consensusPool,
		beaconIndexer:    beaconIndexer,
		chainState:       consensusPool.GetChainState(),
		executionClients: map[*execution.Client]*indexerElClientInfo{},
	}
}

func (ictx *IndexerCtx) AddClientInfo(client *execution.Client, priority int, archive bool) {
	ictx.executionClients[client] = &indexerElClientInfo{
		priority: priority,
		archive:  archive,
	}
}

func (ictx *IndexerCtx) getFinalizedClients(clientType execution.ClientType) []*execution.Client {
	_, finalizedRoot := ictx.consensusPool.GetChainState().GetJustifiedCheckpoint()

	finalizedClients := make([]*execution.Client, 0)
	for _, client := range ictx.executionPool.GetReadyEndpoints(clientType) {
		_, blockHash := client.GetLastHead()
		for _, beaconBlock := range ictx.beaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash)) {
			isInChain, _ := ictx.beaconIndexer.GetBlockDistance(finalizedRoot, beaconBlock.Root)
			if isInChain {
				finalizedClients = append(finalizedClients, client)
				break
			}
		}
	}

	return finalizedClients
}

func (ictx *IndexerCtx) sortClients(clientA *execution.Client, clientB *execution.Client, preferArchive bool) bool {
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

type forkWithClients struct {
	canonical bool
	forkId    beacon.ForkKey
	forkHead  *beacon.ForkHead
	clients   []*execution.Client
}

func (ictx *IndexerCtx) getForksWithClients(clientType execution.ClientType) []*forkWithClients {
	forksWithClients := make([]*forkWithClients, 0)
	forkHeadMap := map[beacon.ForkKey]*beacon.ForkHead{}
	for _, forkHead := range ictx.beaconIndexer.GetForkHeads() {
		forkHeadMap[forkHead.ForkId] = forkHead
	}

	for _, client := range ictx.executionPool.GetReadyEndpoints(clientType) {
		_, blockHash := client.GetLastHead()
		for _, block := range ictx.beaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash)) {

			var matchingForkWithClients *forkWithClients
			for _, forkWithClients := range forksWithClients {
				if forkWithClients.forkId == block.GetForkId() {
					matchingForkWithClients = forkWithClients
					break
				}
			}

			if matchingForkWithClients == nil {
				matchingForkWithClients = &forkWithClients{
					forkId:   block.GetForkId(),
					forkHead: forkHeadMap[block.GetForkId()],
				}
				forksWithClients = append(forksWithClients, matchingForkWithClients)
			}

			matchingForkWithClients.clients = append(matchingForkWithClients.clients, client)
		}
	}

	canonicalHead := ictx.beaconIndexer.GetCanonicalHead(nil)

	for _, forkWithClients := range forksWithClients {
		forkWithClients.canonical = canonicalHead != nil && canonicalHead.GetForkId() == forkWithClients.forkId

		sort.Slice(forkWithClients.clients, func(i, j int) bool {
			return ictx.sortClients(forkWithClients.clients[i], forkWithClients.clients[j], true)
		})
	}

	sort.Slice(forksWithClients, func(i, j int) bool {
		cliA := forksWithClients[i]
		cliB := forksWithClients[j]
		if cliA.canonical != cliB.canonical {
			return cliA.canonical
		}

		return len(cliA.clients) > len(cliB.clients)
	})

	return forksWithClients
}
