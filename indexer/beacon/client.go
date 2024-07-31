package beacon

import (
	"bytes"
	"context"
	"fmt"
	"runtime/debug"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

// Client represents a consensus pool client that should be used for indexing beacon blocks.
type Client struct {
	indexer  *Indexer
	index    uint16
	client   *consensus.Client
	logger   logrus.FieldLogger
	indexing bool

	priority       int
	archive        bool
	skipValidators bool

	blockSubscription *consensus.Subscription[*v1.BlockEvent]
	headSubscription  *consensus.Subscription[*v1.HeadEvent]

	headRoot phase0.Root
}

// newClient creates a new indexer client for a given consensus pool client.
func newClient(index uint16, client *consensus.Client, priority int, archive bool, skipValidators bool, indexer *Indexer, logger logrus.FieldLogger) *Client {
	return &Client{
		indexer:  indexer,
		index:    index,
		client:   client,
		logger:   logger,
		indexing: false,

		priority:       priority,
		archive:        archive,
		skipValidators: skipValidators,
	}
}

// startIndexing starts the indexing process for this client.
// attaches block & head event handlers and starts the event processing subroutine.
func (c *Client) startIndexing() {
	if c.indexing {
		return
	}

	c.indexing = true

	// blocking block subscription with a buffer to ensure no blocks are missed
	c.blockSubscription = c.client.SubscribeBlockEvent(100, true)
	c.headSubscription = c.client.SubscribeHeadEvent(100, true)

	go c.startClientLoop()
}

// startClientLoop starts the client event processing subroutine.
func (c *Client) startClientLoop() {
	defer func() {
		if err := recover(); err != nil {
			c.logger.WithError(err.(error)).Errorf("uncaught panic in indexer.beacon.Client.startClientLoop subroutine: %v, stack: %v", err, string(debug.Stack()))
			time.Sleep(10 * time.Second)

			go c.startClientLoop()
		} else {
			c.indexing = false
		}
	}()

	for {
		err := c.runClientLoop()
		if err != nil {
			c.logger.WithError(err).Warnf("error in indexer.beacon.Client.runClientLoop: %v (retrying in 10 sec)", err)
		}

		time.Sleep(10 * time.Second)
	}
}

// runClientLoop runs the client event processing subroutine.
func (c *Client) runClientLoop() error {
	// 1 - load & process head block
	headSlot, headRoot := c.client.GetLastHead()
	if bytes.Equal(headRoot[:], consensus.NullRoot[:]) {
		c.logger.Debugf("no chain head from client, retrying later")
		return nil
	}

	c.headRoot = headRoot

	headBlock, isNew, err := c.processBlock(headSlot, headRoot, nil)
	if err != nil {
		return fmt.Errorf("failed processing head block: %v", err)
	}

	if headBlock == nil {
		return fmt.Errorf("failed loading head block: %v", err)
	}

	if isNew {
		c.logger.Infof("received block %v:%v [0x%x] head", c.client.GetPool().GetChainState().EpochOfSlot(headSlot), headSlot, headRoot)
	} else {
		c.logger.Debugf("received known block %v:%v [0x%x] head", c.client.GetPool().GetChainState().EpochOfSlot(headSlot), headSlot, headRoot)
	}

	// 2 - backfill old blocks up to the finalization checkpoint or known in cache
	err = c.backfillParentBlocks(headBlock)
	if err != nil {
		c.logger.Errorf("failed backfilling slots: %v", err)
	}

	// 3 - listen to block / head events
	for {
		select {
		case <-c.client.GetContext().Done():
			return nil
		case blockEvent := <-c.blockSubscription.Channel():
			err := c.processBlockEvent(blockEvent)
			if err != nil {
				c.logger.Errorf("failed processing block %v (%v): %v", blockEvent.Slot, blockEvent.Block.String(), err)
			}
		case headEvent := <-c.headSubscription.Channel():
			err := c.processHeadEvent(headEvent)
			if err != nil {
				c.logger.Errorf("failed processing head %v (%v): %v", headEvent.Slot, headEvent.Block.String(), err)
			}
		}
	}

}

// processBlockEvent processes a block event from the event stream.
func (c *Client) processBlockEvent(blockEvent *v1.BlockEvent) error {
	_, err := c.processStreamBlock(blockEvent.Slot, blockEvent.Block)
	return err
}

// processHeadEvent processes a head event from the event stream.
func (c *Client) processHeadEvent(headEvent *v1.HeadEvent) error {
	block, err := c.processStreamBlock(headEvent.Slot, headEvent.Block)
	if err != nil {
		return err
	}

	if bytes.Equal(c.headRoot[:], headEvent.Block[:]) {
		// no head progress?
		return nil
	}

	c.logger.Debugf("head %v -> %v", c.headRoot.String(), block.Root.String())

	parentRoot := block.GetParentRoot()
	if parentRoot != nil && !bytes.Equal(c.headRoot[:], (*parentRoot)[:]) {
		// chain reorg!

		// ensure parents of new head
		err := c.backfillParentBlocks(block)
		if err != nil {
			c.logger.Errorf("failed backfilling slots after reorg: %v", err)
		}

		// find parent of both heads
		oldBlock := c.indexer.blockCache.getBlockByRoot(c.headRoot)
		if oldBlock == nil {
			c.logger.Warnf("can't find old block after reorg.")
		} else if err := c.processReorg(oldBlock, block); err != nil {
			c.logger.Errorf("failed processing reorg: %v", err)
		}
	}

	chainState := c.client.GetPool().GetChainState()
	dependentRoot := headEvent.CurrentDutyDependentRoot

	var dependentBlock *Block
	if !bytes.Equal(dependentRoot[:], consensus.NullRoot[:]) {
		block.dependentRoot = &dependentRoot

		dependentBlock = c.indexer.blockCache.getBlockByRoot(dependentRoot)
		if dependentBlock == nil {
			c.logger.Warnf("dependent block (%v) not found after backfilling", dependentRoot.String())
		}
	} else {
		dependentBlock = c.indexer.blockCache.getDependentBlock(chainState, block, c)
	}

	currentBlock := block
	minInMemorySlot := c.indexer.getMinInMemorySlot()
	for {
		if dependentBlock != nil && currentBlock.Slot >= minInMemorySlot {
			// ensure epoch stats are in loading queue
			epochStats, _ := c.indexer.epochCache.createOrGetEpochStats(chainState.EpochOfSlot(currentBlock.Slot), dependentBlock.Root)
			if !epochStats.addRequestedBy(c) {
				break
			}
		} else {
			break
		}

		currentBlock = dependentBlock
		dependentBlock = c.indexer.blockCache.getDependentBlock(chainState, currentBlock, c)
	}

	c.headRoot = block.Root
	return nil
}

// processStreamBlock processes a block received from the stream (either via block or head events).
func (c *Client) processStreamBlock(slot phase0.Slot, root phase0.Root) (*Block, error) {
	block, isNew, err := c.processBlock(slot, root, nil)
	if err != nil {
		return nil, err
	}

	chainState := c.client.GetPool().GetChainState()

	if isNew {
		c.logger.Infof("received block %v:%v [0x%x] stream", chainState.EpochOfSlot(block.Slot), block.Slot, block.Root[:])
	} else {
		c.logger.Debugf("received known block %v:%v [0x%x] stream", chainState.EpochOfSlot(block.Slot), block.Slot, block.Root[:])
	}

	return block, nil
}

// processReorg processes a chain reorganization.
func (c *Client) processReorg(oldHead *Block, newHead *Block) error {
	// find parent of both heads
	reorgBase := oldHead
	forwardDistance := uint64(0)
	rewindDistance := uint64(0)

	for {
		if res, dist := c.indexer.blockCache.getCanonicalDistance(reorgBase.Root, newHead.Root, 0); res {
			forwardDistance = dist
			break
		}

		parentRoot := reorgBase.GetParentRoot()
		if parentRoot == nil {
			reorgBase = nil
			break
		}

		reorgBase = c.indexer.blockCache.getBlockByRoot(*parentRoot)
		if reorgBase == nil {
			break
		}

		rewindDistance++
	}

	if rewindDistance == 0 {
		return nil // just a fast forward
	}

	c.logger.Infof("chain reorg! depth: -%v / +%v (old: %v, new: %v)", rewindDistance, forwardDistance, oldHead.Root.String(), newHead.Root.String())

	// TODO: do something with reorgs?

	return nil
}

// processBlock processes a block (from stream & polling).
func (c *Client) processBlock(slot phase0.Slot, root phase0.Root, header *phase0.SignedBeaconBlockHeader) (block *Block, isNew bool, err error) {
	chainState := c.client.GetPool().GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()

	if slot < finalizedSlot {
		// block is in finalized epoch
		// known block or a new orphaned block

		// don't add to cache, process this block right after loading the details
		block = newBlock(c.indexer.dynSsz, root, slot)

		dbBlockHead := db.GetBlockHeadByRoot(root[:])
		if dbBlockHead != nil {
			block.isInFinalizedDb = true
			block.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
		}

	} else {
		block, _ = c.indexer.blockCache.createOrGetBlock(root, slot)
	}

	err = block.EnsureHeader(func() (*phase0.SignedBeaconBlockHeader, error) {
		if header != nil {
			return header, nil
		}

		return c.loadHeader(root)
	})
	if err != nil {
		return
	}

	isNew, err = block.EnsureBlock(func() (*spec.VersionedSignedBeaconBlock, error) {
		return c.loadBlock(root)
	})
	if err != nil {
		return
	}

	if slot >= finalizedSlot && isNew {
		// fork detection
		forkId, err2 := c.indexer.forkCache.processBlock(block)
		block.forkId = forkId

		if err2 != nil {
			c.logger.Warnf("failed processing new fork: %v", err2)
		}

		// insert into unfinalized blocks
		var dbBlock *dbtypes.UnfinalizedBlock
		dbBlock, err = block.buildUnfinalizedBlock()
		if err != nil {
			return
		}

		// write to db
		err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
			err := db.InsertUnfinalizedBlock(dbBlock, tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return
		}

		block.isInUnfinalizedDb = true
	}
	if slot < finalizedSlot && !block.isInFinalizedDb {
		// process new orphaned block in finalized epoch
		// TODO: insert new orphaned block to db
	}

	return
}

// loadHeader loads the block header from the RPC client.
func (c *Client) loadHeader(root phase0.Root) (*phase0.SignedBeaconBlockHeader, error) {
	ctx, cancel := context.WithTimeout(c.client.GetContext(), BeaconHeaderRequestTimeout)
	defer cancel()

	header, err := c.client.GetRPCClient().GetBlockHeaderByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return header.Header, nil
}

// loadBlock loads the block body from the RPC client.
func (c *Client) loadBlock(root phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	ctx, cancel := context.WithTimeout(c.client.GetContext(), BeaconBodyRequestTimeout)
	defer cancel()

	body, err := c.client.GetRPCClient().GetBlockBodyByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// backfillParentBlocks backfills parent blocks up to the finalization checkpoint or known in cache.
func (c *Client) backfillParentBlocks(headBlock *Block) error {
	chainState := c.client.GetPool().GetChainState()

	// walk backwards and load all blocks until we reach a block that is marked as seen by this client or is smaller than finalized
	parentRoot := *headBlock.GetParentRoot()
	for {
		var parentHead *phase0.SignedBeaconBlockHeader
		parentBlock := c.indexer.blockCache.getBlockByRoot(parentRoot)
		if parentBlock != nil {
			parentBlock.seenMutex.RLock()
			isSeen := parentBlock.seenMap[c.index] != nil
			parentBlock.seenMutex.RUnlock()

			if isSeen {
				break
			}

			parentHead = parentBlock.GetHeader()
		}

		if parentHead == nil {
			headerRsp, err := c.loadHeader(parentRoot)
			if err != nil {
				return fmt.Errorf("could not load parent header [0x%x]: %v", parentRoot, err)
			}
			if headerRsp == nil {
				return fmt.Errorf("could not find parent header [0x%x]", parentRoot)
			}

			parentHead = headerRsp
		}

		parentSlot := parentHead.Message.Slot
		isNewBlock := false

		if parentSlot < chainState.GetFinalizedSlot() {
			c.logger.Debugf("backfill cache: reached finalized slot %v:%v [0x%x]", chainState.EpochOfSlot(parentSlot), parentSlot, parentRoot)
			break
		}

		if parentBlock == nil {
			var err error

			parentBlock, isNewBlock, err = c.processBlock(parentSlot, parentRoot, parentHead)
			if err != nil {
				return fmt.Errorf("could not process block [0x%x]: %v", parentRoot, err)
			}
		}

		if isNewBlock {
			c.logger.Infof("received block %v:%v [0x%x] backfill", chainState.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		} else {
			c.logger.Debugf("received known block %v:%v [0x%x] backfill", chainState.EpochOfSlot(parentSlot), parentSlot, parentRoot)
		}

		if parentSlot == 0 {
			c.logger.Debugf("backfill cache: reached gensis slot [0x%x]", parentRoot)
			break
		}

		parentRootPtr := parentBlock.GetParentRoot()
		if parentRootPtr != nil {
			parentRoot = *parentRootPtr
		} else {
			parentRoot = parentHead.Message.ParentRoot
		}

		if bytes.Equal(parentRoot[:], consensus.NullRoot[:]) {
			c.logger.Infof("backfill cache: reached null root (genesis)")
			break
		}
	}
	return nil
}
