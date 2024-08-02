package beacon

import (
	"bytes"
	"math/rand/v2"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

func (indexer *Indexer) GetReadyClientsByCheckpoint(finalizedRoot phase0.Root) []*Client {
	clients := make([]*Client, 0)

	for _, client := range indexer.clients {
		if client.client.GetStatus() != consensus.ClientStatusOnline {
			continue
		}

		_, root, _, _ := client.client.GetFinalityCheckpoint()
		if !bytes.Equal(root[:], finalizedRoot[:]) {
			continue
		}

		clients = append(clients, client)
	}

	rand.Shuffle(len(clients), func(i, j int) {
		clients[i], clients[j] = clients[j], clients[i]
	})

	return clients
}

func (indexer *Indexer) GetReadyClientsByBlockRoot(blockRoot phase0.Root) []*Client {
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

	rand.Shuffle(len(clients), func(i, j int) {
		clients[i], clients[j] = clients[j], clients[i]
	})

	return clients
}

func (indexer *Indexer) GetReadyClientByBlockRoot(blockRoot phase0.Root) *Client {
	clients := indexer.GetReadyClientsByBlockRoot(blockRoot)
	if len(clients) > 0 {
		return clients[0]
	}

	return nil
}

func (indexer *Indexer) GetBlockByRoot(blockRoot phase0.Root) *Block {
	return indexer.blockCache.getBlockByRoot(blockRoot)
}
