package indexer

import (
	"time"

	"github.com/ethpandaops/dora/utils"
)

type DepositIndexer struct {
	indexer *Indexer
}

func newDepositIndexer(indexer *Indexer) *DepositIndexer {
	ds := &DepositIndexer{
		indexer: indexer,
	}

	go ds.runDepositIndexerLoop()

	return ds
}

func (ds *DepositIndexer) runDepositIndexerLoop() {
	defer utils.HandleSubroutinePanic("runCacheLoop")

	for {
		time.Sleep(60 * time.Second)
		logger.Debugf("run deposit indexer logic")

		err := ds.runDepositIndexer()
		if err != nil {
			logger.Errorf("deposit indexer error: %v", err)
		}
	}
}

func (ds *DepositIndexer) runDepositIndexer() error {
	return nil
}
