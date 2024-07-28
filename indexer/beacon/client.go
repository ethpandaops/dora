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
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

type Client struct {
	indexer    *Indexer
	index      uint16
	client     *consensus.Client
	logger     logrus.FieldLogger
	blockCache *blockCache
	indexing   bool

	priority       int
	archive        bool
	skipValidators bool

	blockSubscription *consensus.Subscription[*v1.BlockEvent]
}

func newClient(index uint16, client *consensus.Client, priority int, archive bool, skipValidators bool, indexer *Indexer, logger logrus.FieldLogger, blockCache *blockCache) *Client {
	return &Client{
		indexer:    indexer,
		index:      index,
		client:     client,
		logger:     logger,
		blockCache: blockCache,
		indexing:   false,

		priority:       priority,
		archive:        archive,
		skipValidators: skipValidators,
	}
}

func (c *Client) startIndexing() {
	if c.indexing {
		return
	}

	c.indexing = true

	// blocking block subscription with a buffer to ensure no blocks are missed
	c.blockSubscription = c.client.SubscribeBlockEvent(100, true)

	go c.startClientLoop()
}

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

func (c *Client) runClientLoop() error {
	// 1 - load & process head block
	headSlot, headRoot := c.client.GetLastHead()
	if bytes.Equal(headRoot[:], nullRoot[:]) {
		c.logger.Debugf("no chain head from client, retrying later")
		return nil
	}

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

	// 3 - listen to block events
	for blockEvent := range c.blockSubscription.Channel() {
		err := c.processBlockEvent(blockEvent)
		if err != nil {
			c.logger.Errorf("failed processing block %v (%v): %v", blockEvent.Slot, blockEvent.Block.String(), err)
		}
	}

	return nil
}

func (c *Client) processBlockEvent(blockEvent *v1.BlockEvent) error {
	block, isNew, err := c.processBlock(blockEvent.Slot, blockEvent.Block, nil)
	if err != nil {
		return err
	}

	chainState := c.client.GetPool().GetChainState()

	if isNew {
		c.logger.Infof("received block %v:%v [0x%x] stream", chainState.EpochOfSlot(block.Slot), block.Slot, block.Root[:])
	} else {
		c.logger.Debugf("received known block %v:%v [0x%x] stream", chainState.EpochOfSlot(block.Slot), block.Slot, block.Root[:])
	}

	return nil
}

func (c *Client) processBlock(slot phase0.Slot, root phase0.Root, header *phase0.SignedBeaconBlockHeader) (block *Block, isNew bool, err error) {
	chainState := c.client.GetPool().GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()

	if slot < finalizedSlot {
		// block is in finalized epoch
		// known block or a new orphaned block

		// don't add to cache, process this block right after loading the details
		block = newBlock(root, slot)

		dbBlockHead := db.GetBlockHeadByRoot(root[:])
		if dbBlockHead != nil {
			block.isInFinalizedDb = true
			block.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
		}

	} else {
		block, _ = c.blockCache.createOrGetBlock(root, slot)
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
		// insert into unfinalized blocks
		err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
			dbBlock, err := block.buildUnfinalizedBlock(chainState.GetSpecs())
			if err != nil {
				return err
			}

			return db.InsertUnfinalizedBlock(dbBlock, tx)
		})
		if err != nil {
			return
		}

		block.isInUnfinalizedDb = true

		if block.Slot < chainState.EpochToSlot(c.indexer.getMinInEpochSlot()) {
			// prune block body from memory
			block.block = nil
		}
	}
	if slot < finalizedSlot && !block.isInFinalizedDb {
		// process new orphaned block in finalized epoch
		// TODO: insert new orphaned block to db
	}

	return
}

func (c *Client) loadHeader(root phase0.Root) (*phase0.SignedBeaconBlockHeader, error) {
	ctx, cancel := context.WithTimeout(c.client.GetContext(), BeaconHeaderRequestTimeout)
	defer cancel()

	header, err := c.client.GetRPCClient().GetBlockHeaderByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return header.Header, nil
}

func (c *Client) loadBlock(root phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	ctx, cancel := context.WithTimeout(c.client.GetContext(), BeaconBodyRequestTimeout)
	defer cancel()

	body, err := c.client.GetRPCClient().GetBlockBodyByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func (c *Client) backfillParentBlocks(headBlock *Block) error {
	chainState := c.client.GetPool().GetChainState()

	// walk backwards and load all blocks until we reach a block that is marked as seen by this client or is smaller than finalized
	parentRoot := *headBlock.GetParentRoot()
	for {
		var parentHead *phase0.SignedBeaconBlockHeader
		parentBlock := c.blockCache.getBlockByRoot(parentRoot)
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

		if parentSlot <= chainState.GetFinalizedSlot() {
			c.logger.Debugf("backfill cache: reached finalized slot %v:%v [0x%x]", chainState.EpochOfSlot(parentSlot), parentSlot, parentRoot)
			break
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

		if bytes.Equal(parentRoot[:], nullRoot[:]) {
			c.logger.Infof("backfill cache: reached null root (genesis)")
			break
		}
	}
	return nil
}
