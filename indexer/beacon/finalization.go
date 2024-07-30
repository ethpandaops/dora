package beacon

import (
	"runtime/debug"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

type finalizationWorker struct {
	indexer              *Indexer
	lastFinalizedRoot    phase0.Root
	finalitySubscription *consensus.Subscription[*v1.Finality]
}

func newFinalizationWorker(indexer *Indexer) *finalizationWorker {
	_, finalizedRoot := indexer.consensusPool.GetChainState().GetFinalizedCheckpoint()
	fw := &finalizationWorker{
		indexer:              indexer,
		lastFinalizedRoot:    finalizedRoot,
		finalitySubscription: indexer.consensusPool.SubscribeFinalizedEvent(10),
	}

	go fw.runFinalizationLoop()

	return fw
}

func (fw *finalizationWorker) runFinalizationLoop() {
	defer func() {
		if err := recover(); err != nil {
			fw.indexer.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.finalizationWorker.runFinalizationLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go fw.runFinalizationLoop()
		}
	}()

	for {
		select {
		case finalityEvent := <-fw.finalitySubscription.Channel():
			err := fw.processFinalityEvent(finalityEvent)
			if err != nil {
				fw.indexer.logger.WithError(err).Errorf("error processing finality event (epoch: %v, root: %v)", finalityEvent.Finalized.Epoch, finalityEvent.Finalized.Root.String())
			}
		}

	}
}

func (fw *finalizationWorker) processFinalityEvent(finalityEvent *v1.Finality) error {
	fw.indexer.logger.Infof("finality event! %v %v", finalityEvent.Finalized.Epoch, finalityEvent.Finalized.Root.String())
	return nil
}
