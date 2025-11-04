package execution

import (
	"context"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/sirupsen/logrus"
)

// IndexerCtx holds shared context for all execution indexers
type IndexerCtx struct {
	ctx            context.Context
	logger         logrus.FieldLogger
	executionPool  *execution.Pool
	consensusPool  *consensus.Pool
	beaconIndexer  *beacon.Indexer
	chainState     *consensus.ChainState
	elIndexer      *ElIndexer
}

// NewIndexerCtx creates a new indexer context
func NewIndexerCtx(logger logrus.FieldLogger, executionPool *execution.Pool, consensusPool *consensus.Pool, beaconIndexer *beacon.Indexer) *IndexerCtx {
	return &IndexerCtx{
		ctx:            context.Background(),
		logger:         logger,
		executionPool:  executionPool,
		consensusPool:  consensusPool,
		beaconIndexer:  beaconIndexer,
		chainState:     consensusPool.GetChainState(),
	}
}

// GetElIndexer returns the execution layer indexer
func (ctx *IndexerCtx) GetElIndexer() *ElIndexer {
	return ctx.elIndexer
}

// AddClientInfo adds client information (for compatibility with existing deposit/withdrawal indexers)
func (ctx *IndexerCtx) AddClientInfo(client *execution.Client, priority int, archive bool) {
	// This method exists for compatibility with deposit/withdrawal indexers
	// The execution pool already manages client priorities
}
