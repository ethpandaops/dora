package beacon

import (
	v1 "github.com/attestantio/go-eth2-client/api/v1"
)

func (indexer *Indexer) processFinalityEvent(finalityEvent *v1.Finality) error {
	indexer.logger.Infof("finality event! %v %v", finalityEvent.Finalized.Epoch, finalityEvent.Finalized.Root.String())
	return nil
}
