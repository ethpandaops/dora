package beacon

import (
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"

	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// The slot metadata (inclusion lists, execution proofs) is collected in memory
// for the bid cache's flush window. When the flush drops a slot, the caches
// write the index values of the blocks they concern into Block.metadata, which
// is persisted with the unfinalized block row and restored with it, so no
// index value is ever looked up in a store. Every collected item is written to
// the unfinalized_metadata table on the slot tick as crash copy; the items of
// a slot are removed once its meta object is in the blockdb, or kept for
// metadataHistoryLength epochs where no blockdb takes them over.

// blockMetadataValue returns one index value of a block: from its aggregated
// metadata, 0 while the block's slot is still collected (nothing observed), and
// unknown for a block the caches never held (built by the synchronizer).
func (indexer *Indexer) blockMetadataValue(block *Block, pick func(metadata *BlockMetadata) int16) int16 {
	if metadata := block.GetMetadata(); metadata != nil {
		return pick(metadata)
	}
	if uint64(block.Slot) >= indexer.metadataCutoff.Load() {
		return 0
	}
	return dbtypes.MetadataUnknown
}

// persistBlockMetadata writes the aggregated metadata of the given blocks to
// their unfinalized block rows.
func (indexer *Indexer) persistBlockMetadata(blocks []*Block) {
	if len(blocks) == 0 {
		return
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		seen := make(map[phase0.Root]bool, len(blocks))
		for _, block := range blocks {
			if seen[block.Root] || !block.isInUnfinalizedDb {
				continue
			}
			seen[block.Root] = true

			if err := db.UpdateUnfinalizedBlockMetadata(indexer.ctx, tx, block.Root[:], block.encodedMetadata()); err != nil {
				return fmt.Errorf("block 0x%x: %w", block.Root, err)
			}
		}
		return nil
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("error persisting block metadata")
	}
}

// persistUnfinalizedMetadata writes the crash copies of the items collected
// since the last call.
func (indexer *Indexer) persistUnfinalizedMetadata() {
	listEntries := indexer.inclusionListCache.collectDirtyEntries()
	proofEntries := indexer.executionProofCache.collectDirtyEntries()
	if len(listEntries) == 0 && len(proofEntries) == 0 {
		return
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for _, entry := range append(listEntries, proofEntries...) {
			if err := db.UpsertUnfinalizedMetadata(indexer.ctx, tx, entry); err != nil {
				return fmt.Errorf("slot %d kind %d: %w", entry.Slot, entry.Kind, err)
			}
		}
		return nil
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("error persisting unfinalized metadata")
		// retried with the next tick
		indexer.inclusionListCache.markDirtyEntries(listEntries)
		indexer.executionProofCache.markDirtyEntries(proofEntries)
	}
}

// deleteUnfinalizedMetadata removes the crash copies of slots whose meta
// objects are in the blockdb: the lists and evaluations of listSlots, the
// proofs of proofSlots.
func (indexer *Indexer) deleteUnfinalizedMetadata(listSlots []uint64, proofSlots []uint64) {
	if len(listSlots) == 0 && len(proofSlots) == 0 {
		return
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		if err := db.DeleteUnfinalizedMetadata(indexer.ctx, tx, listSlots, []dbtypes.UnfinalizedMetadataKind{
			dbtypes.UnfinalizedMetadataInclusionList, dbtypes.UnfinalizedMetadataInclusionListEval,
		}); err != nil {
			return err
		}
		return db.DeleteUnfinalizedMetadata(indexer.ctx, tx, proofSlots, []dbtypes.UnfinalizedMetadataKind{
			dbtypes.UnfinalizedMetadataExecutionProof,
		})
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("error deleting unfinalized metadata")
	}
}

// cleanupUnfinalizedMetadata removes the items older than metadataHistoryLength
// epochs: the retention of the table where no blockdb takes the objects over,
// and of items a crash left behind after their object was written.
func (indexer *Indexer) cleanupUnfinalizedMetadata(currentSlot phase0.Slot) {
	chainState := indexer.consensusPool.GetChainState()
	specs := chainState.GetSpecs()
	if specs == nil {
		return
	}

	retention := phase0.Slot(uint64(indexer.metadataHistoryLength) * specs.SlotsPerEpoch)
	if currentSlot <= retention {
		return
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.DeleteUnfinalizedMetadataBefore(indexer.ctx, tx, uint64(currentSlot-retention))
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("error cleaning up unfinalized metadata")
	}
}

// restoreUnfinalizedMetadata restores the metadata caches after a start from
// the crash copies: the items of the collection window, and with a blockdb the
// items of older slots left behind by a crash, which the next flush then writes.
// Without blockdb the older items are the store and stay where they are.
func (indexer *Indexer) restoreUnfinalizedMetadata(finalizedSlot phase0.Slot, currentSlot phase0.Slot) {
	windowStart := phase0.Slot(0)
	if currentSlot > bidCacheRetainSlots {
		windowStart = currentSlot - bidCacheRetainSlots
	}
	indexer.metadataCutoff.Store(uint64(windowStart))

	fromSlot := windowStart
	if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsSlotMeta() && finalizedSlot < fromSlot {
		fromSlot = finalizedSlot
	}

	restored := 0
	err := db.StreamUnfinalizedMetadata(indexer.ctx, uint64(fromSlot), func(entry *dbtypes.UnfinalizedMetadata) {
		var err error
		switch entry.Kind {
		case dbtypes.UnfinalizedMetadataExecutionProof:
			err = indexer.executionProofCache.restoreEntry(entry)
		default:
			err = indexer.inclusionListCache.restoreEntry(entry)
		}
		if err != nil {
			indexer.logger.Warnf("error restoring unfinalized metadata %d/%d: %v", entry.Slot, entry.Kind, err)
			return
		}
		restored++
	})
	if err != nil {
		indexer.logger.WithError(err).Errorf("error restoring unfinalized metadata")
	}

	// the blocks of the window are collected further
	indexer.executionProofCache.trackBlocks(indexer.blockCache.getBlocksBySlotRange(windowStart, currentSlot))

	if restored > 0 {
		indexer.logger.Infof("restored %d unfinalized metadata items from DB (slots >= %d)", restored, fromSlot)
	}
}
