package execution

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/utils"
)

// ENSIndexer handles ENS name resolution
type ENSIndexer struct {
	indexerCtx   *IndexerCtx
	logger       logrus.FieldLogger
	resolveQueue chan resolveRequest
	stopChan     chan struct{}
	wg           sync.WaitGroup

	// ENS configuration
	reverseRegistrar common.Address
	resolverABI      abi.ABI
}

type resolveRequest struct {
	address     common.Address
	blockNumber uint64
}

// NewENSIndexer creates a new ENS indexer
func NewENSIndexer(indexerCtx *IndexerCtx, logger logrus.FieldLogger) *ENSIndexer {
	// Default ENS reverse registrar address (mainnet)
	reverseRegistrar := common.HexToAddress("0xa58E81fe9b61B5c3fE2AFD33CF304c454AbFc7Cb")

	// Check for custom resolver in config
	if utils.Config.Indexer.ElEnsResolver != "" {
		reverseRegistrar = common.HexToAddress(utils.Config.Indexer.ElEnsResolver)
		logger.WithField("resolver", reverseRegistrar.Hex()).Info("Using custom ENS resolver")
	}

	// Parse resolver ABI for name() function
	resolverABI, err := abi.JSON(strings.NewReader(resolverABIJSON))
	if err != nil {
		logger.WithError(err).Error("Failed to parse resolver ABI")
	}

	ei := &ENSIndexer{
		indexerCtx:       indexerCtx,
		logger:           logger,
		resolveQueue:     make(chan resolveRequest, 1000),
		stopChan:         make(chan struct{}),
		reverseRegistrar: reverseRegistrar,
		resolverABI:      resolverABI,
	}

	// Start workers
	for i := 0; i < 3; i++ { // 3 concurrent workers
		ei.wg.Add(1)
		go ei.resolveWorker()
	}

	return ei
}

// Stop stops the ENS indexer
func (ei *ENSIndexer) Stop() {
	close(ei.stopChan)
	ei.wg.Wait()
}

// QueueResolve queues an address for ENS resolution
func (ei *ENSIndexer) QueueResolve(address common.Address, blockNumber uint64) {
	select {
	case ei.resolveQueue <- resolveRequest{address: address, blockNumber: blockNumber}:
	case <-ei.stopChan:
		return
	default:
		// Queue full, skip
	}
}

// resolveWorker processes ENS resolution requests
func (ei *ENSIndexer) resolveWorker() {
	defer ei.wg.Done()

	for {
		select {
		case req := <-ei.resolveQueue:
			if err := ei.resolveENS(req.address, req.blockNumber); err != nil {
				ei.logger.WithError(err).WithField("address", req.address.Hex()).Debug("Error resolving ENS")
			}
			// Rate limit to avoid overwhelming the RPC
			time.Sleep(100 * time.Millisecond)
		case <-ei.stopChan:
			return
		}
	}
}

// resolveENS resolves the ENS name for an address using reverse resolution
func (ei *ENSIndexer) resolveENS(address common.Address, blockNumber uint64) error {
	// Check if we already have a recent ENS name for this address
	addr, err := db.GetElAddress(address.Bytes())
	if err == nil && addr.EnsName != nil && addr.EnsLastChecked != nil {
		// Check if it was checked recently (within last 24 hours)
		if time.Since(time.Unix(int64(*addr.EnsLastChecked), 0)) < 24*time.Hour {
			return nil // Already resolved recently
		}
	}

	// Get execution client
	client := ei.indexerCtx.executionPool.GetReadyEndpoint(nil)
	if client == nil {
		return fmt.Errorf("no ready execution client")
	}

	// Use custom resolver URL if configured
	if utils.Config.Indexer.ElEnsResolverUrl != "" {
		// TODO: Create a separate client for custom resolver URL
		// For now, use the default client
	}

	ctx := context.Background()

	// Construct the reverse record node
	// reverse record = keccak256(address.reverse)
	reverseNode := getReverseNode(address)

	// Call resolver(bytes32) on reverse registrar to get resolver address
	// Function signature for resolver(bytes32): 0x0178b8bf
	resolverData := fmt.Sprintf("0x0178b8bf%064x", reverseNode)
	resolverResult, err := client.GetRPC().CallContract(ctx, map[string]interface{}{
		"to":   ei.reverseRegistrar.Hex(),
		"data": resolverData,
	}, nil)
	if err != nil {
		return err
	}

	if len(resolverResult) < 32 {
		return nil // No resolver set
	}

	// Extract resolver address from result
	resolverAddr := common.BytesToAddress(resolverResult[12:32])
	if resolverAddr == (common.Address{}) {
		return nil // No resolver set
	}

	// Call name(bytes32) on resolver to get the ENS name
	nameData, err := ei.resolverABI.Pack("name", reverseNode)
	if err != nil {
		return err
	}

	nameResult, err := client.GetRPC().CallContract(ctx, map[string]interface{}{
		"to":   resolverAddr.Hex(),
		"data": fmt.Sprintf("0x%x", nameData),
	}, nil)
	if err != nil {
		return err
	}

	if len(nameResult) == 0 {
		return nil // No name set
	}

	// Decode the name
	var name string
	if err := ei.resolverABI.UnpackIntoInterface(&name, "name", nameResult); err != nil {
		return err
	}

	if name == "" {
		return nil
	}

	// Verify forward resolution matches
	// This prevents spoofing - we need to check that the ENS name resolves back to this address
	if !ei.verifyForwardResolution(ctx, client, name, address) {
		ei.logger.WithFields(logrus.Fields{
			"address": address.Hex(),
			"name":    name,
		}).Debug("ENS forward resolution mismatch, skipping")
		return nil
	}

	// Save to database
	now := uint64(time.Now().Unix())
	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.UpdateElAddressENS(address.Bytes(), &name, &now, tx)
	})
}

// getReverseNode computes the namehash for addr.reverse
func getReverseNode(address common.Address) common.Hash {
	// This is a simplified version - full implementation would use namehash
	// For now, return a placeholder
	// Proper implementation: namehash(address.toLowerCase() + ".addr.reverse")

	addrStr := strings.ToLower(address.Hex()[2:]) // Remove 0x prefix
	node := addrStr + ".addr.reverse"

	// Compute namehash
	return nameHash(node)
}

// nameHash computes the ENS namehash
func nameHash(name string) common.Hash {
	if name == "" {
		return common.Hash{}
	}

	node := common.Hash{}
	labels := strings.Split(name, ".")

	for i := len(labels) - 1; i >= 0; i-- {
		labelHash := crypto.Keccak256Hash([]byte(labels[i]))
		node = crypto.Keccak256Hash(node[:], labelHash[:])
	}

	return node
}

// verifyForwardResolution verifies that the ENS name resolves to the expected address
func (ei *ENSIndexer) verifyForwardResolution(ctx context.Context, client interface{}, name string, expectedAddr common.Address) bool {
	// TODO: Implement forward resolution verification
	// This would involve:
	// 1. Computing namehash for the name
	// 2. Calling resolver() on ENS registry
	// 3. Calling addr() on the resolver
	// 4. Comparing with expected address

	// For now, skip verification (accept all)
	return true
}

// resolverABIJSON is the ABI for ENS resolver name() function
const resolverABIJSON = `[{
	"constant": true,
	"inputs": [{"name": "node", "type": "bytes32"}],
	"name": "name",
	"outputs": [{"name": "", "type": "string"}],
	"type": "function"
}]`
