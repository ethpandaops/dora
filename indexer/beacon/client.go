package beacon

import (
	"bytes"
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
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

	blockSubscription               *utils.Subscription[*v1.BlockEvent]
	headSubscription                *utils.Subscription[*v1.HeadEvent]
	executionPayloadSubscription    *utils.Subscription[*v1.ExecutionPayloadAvailableEvent]
	executionPayloadBidSubscription *utils.Subscription[*gloas.SignedExecutionPayloadBid]

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

func (c *Client) getContext() context.Context {
	return c.client.GetContext()
}

func (c *Client) GetClient() *consensus.Client {
	return c.client
}

func (c *Client) GetPriority() int {
	return c.priority
}

func (c *Client) GetIndex() uint16 {
	return c.index
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
	c.executionPayloadSubscription = c.client.SubscribeExecutionPayloadAvailableEvent(100, true)
	c.executionPayloadBidSubscription = c.client.SubscribeExecutionPayloadBidEvent(100, true)

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

func (c *Client) emitBlockLogEntry(slot phase0.Slot, root phase0.Root, source string, isNew bool, forkId ForkKey, processingTimes []time.Duration) {
	chainState := c.client.GetPool().GetChainState()

	processingTimesStr := ""
	if len(processingTimes) > 0 {
		str := strings.Builder{}
		str.WriteString(" (")
		for i, pt := range processingTimes {
			if i > 0 {
				str.WriteString(", ")
			}

			str.WriteString(fmt.Sprintf("%v ms", pt.Milliseconds()))
		}
		str.WriteString(")")

		processingTimesStr = str.String()
	}

	if isNew {
		c.logger.Infof("received block %v:%v [0x%x] %v %v fork: %v", chainState.EpochOfSlot(slot), slot, root[:], source, processingTimesStr, forkId)
	} else {
		c.logger.Debugf("received known block %v:%v [0x%x] %v %v fork: %v", chainState.EpochOfSlot(slot), slot, root[:], source, processingTimesStr, forkId)
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

	headBlock, isNew, processingTimes, err := c.processBlock(headSlot, headRoot, nil, false, true)
	if err != nil {
		return fmt.Errorf("failed processing head block: %v", err)
	}

	if headBlock == nil {
		return fmt.Errorf("failed loading head block: %v", err)
	}

	c.emitBlockLogEntry(headSlot, headRoot, "head", isNew, headBlock.forkId, processingTimes)

	// 2 - backfill old blocks up to the finalization checkpoint or known in cache
	err = c.indexer.withBackfillTracker(func() error {
		return c.backfillParentBlocks(headBlock)
	})
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
		case executionPayloadEvent := <-c.executionPayloadSubscription.Channel():
			err := c.processExecutionPayloadAvailableEvent(executionPayloadEvent)
			if err != nil {
				c.logger.Errorf("failed processing execution payload %v (%v): %v", executionPayloadEvent.Slot, executionPayloadEvent.BlockRoot.String(), err)
			}
		case executionPayloadBidEvent := <-c.executionPayloadBidSubscription.Channel():
			err := c.processExecutionPayloadBidEvent(executionPayloadBidEvent)
			if err != nil {
				c.logger.Errorf("failed processing execution payload bid %v (%v): %v", executionPayloadBidEvent.Message.Slot, executionPayloadBidEvent.Message.ParentBlockRoot.String(), err)
			}
		}
	}

}

// processBlockEvent processes a block event from the event stream.
func (c *Client) processBlockEvent(blockEvent *v1.BlockEvent) error {
	if c.client.GetStatus() != consensus.ClientStatusOnline && c.client.GetStatus() != consensus.ClientStatusOptimistic {
		// client is not ready, skip
		return nil
	}

	_, err := c.processStreamBlock(blockEvent.Slot, blockEvent.Block)
	return err
}

// processHeadEvent processes a head event from the event stream.
func (c *Client) processHeadEvent(headEvent *v1.HeadEvent) error {
	if c.client.GetStatus() != consensus.ClientStatusOnline && c.client.GetStatus() != consensus.ClientStatusOptimistic {
		finalizedSlot := c.client.GetPool().GetChainState().GetFinalizedSlot()
		if headEvent.Slot < finalizedSlot {
			c.logger.Debugf("head event %v:%v is in the past, skipping", headEvent.Slot, headEvent.Block.String())
			return nil
		}
	}

	block, err := c.processStreamBlock(headEvent.Slot, headEvent.Block)
	if err != nil {
		return err
	}

	if bytes.Equal(c.headRoot[:], headEvent.Block[:]) {
		// no head progress?
		return nil
	}

	c.logger.Debugf("head %v -> %v", c.headRoot.String(), block.Root.String())

	// check for chain reorgs
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
	if !bytes.Equal(dependentRoot[:], consensus.NullRoot[:]) {
		block.dependentRoot = &dependentRoot
	}

	// walk back the chain of epoch stats to ensure we have all duties & epoch specific data for the clients chain
	currentBlock := block
	headEpoch := chainState.EpochOfSlot(currentBlock.Slot)
	currentEpoch := headEpoch
	minInMemorySlot := c.indexer.getMinInMemorySlot()
	absoluteMinInMemoryEpoch := c.indexer.getAbsoluteMinInMemoryEpoch()
	for {
		parentRoot := currentBlock.GetParentRoot()
		if parentRoot == nil {
			break
		}

		isEpochStart := false
		parentBlock := c.indexer.blockCache.getBlockByRoot(*parentRoot)

		if currentBlock.Slot == 0 {
			isEpochStart = true
		} else if currentBlock.dependentRoot != nil && *parentRoot == *currentBlock.dependentRoot && (parentBlock == nil || parentBlock.Slot > 0) {
			isEpochStart = true
		} else if parentBlock != nil && chainState.EpochOfSlot(parentBlock.Slot) < currentEpoch {
			isEpochStart = true
		}

		if isEpochStart {
			epoch := chainState.EpochOfSlot(currentBlock.Slot)
			dependentRoot := *parentRoot

			// ensure epoch stats for the epoch
			epochStats := c.indexer.epochCache.createOrGetEpochStats(epoch, dependentRoot)

			if epoch >= absoluteMinInMemoryEpoch {
				c.indexer.epochCache.ensureEpochDependentState(epochStats, currentBlock.Root)
			}
			if !epochStats.addRequestedBy(c) {
				break
			}
		}

		if parentBlock == nil || parentBlock.Slot < minInMemorySlot {
			break
		}

		currentBlock = parentBlock
		currentEpoch = chainState.EpochOfSlot(currentBlock.Slot)
	}

	c.headRoot = block.Root
	return nil
}

// processStreamBlock processes a block received from the stream (either via block or head events).
func (c *Client) processStreamBlock(slot phase0.Slot, root phase0.Root) (*Block, error) {
	block, isNew, processingTimes, err := c.processBlock(slot, root, nil, true, false)
	if err != nil {
		return nil, err
	}

	c.emitBlockLogEntry(slot, root, "stream", isNew, block.forkId, processingTimes)

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
		c.logger.Debugf("chain fast forward! +%v slots (old: %v, new: %v)", forwardDistance, oldHead.Root.String(), newHead.Root.String())
		return nil // just a fast forward
	}
	if forwardDistance == 0 {
		c.logger.Debugf("chain rewind! -%v slots (old: %v, new: %v)", rewindDistance, oldHead.Root.String(), newHead.Root.String())
		return nil // just a rewind
	}

	c.logger.Infof("chain reorg! depth: -%v / +%v (old: %v, new: %v)", rewindDistance, forwardDistance, oldHead.Root.String(), newHead.Root.String())

	// TODO: do something with reorgs?

	return nil
}

// processBlock processes a block (from stream & polling).
func (c *Client) processBlock(slot phase0.Slot, root phase0.Root, header *phase0.SignedBeaconBlockHeader, trackRecvDelay bool, loadPayload bool) (block *Block, isNew bool, processingTimes []time.Duration, err error) {
	chainState := c.client.GetPool().GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()
	processingTimes = make([]time.Duration, 3)

	if slot < finalizedSlot {
		// block is in finalized epoch
		// known block or a new orphaned block

		// don't add to cache, process this block right after loading the details
		block = newBlock(c.indexer.dynSsz, root, slot, 0)

		dbBlockHead := db.GetBlockHeadByRoot(root[:])
		if dbBlockHead != nil {
			block.isInFinalizedDb = true
			block.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
		}

	} else {
		block, _ = c.indexer.blockCache.createOrGetBlock(root, slot)
	}

	recvDelay := int32(0)
	if trackRecvDelay {
		slotTime := chainState.SlotToTime(slot)
		recvDelay = int32(time.Since(slotTime).Milliseconds())
	}

	block.SetSeenBy(c, recvDelay)

	err = block.EnsureHeader(func() (*phase0.SignedBeaconBlockHeader, error) {
		if header != nil {
			return header, nil
		}

		t1 := time.Now()
		defer func() {
			processingTimes[0] += time.Since(t1)
		}()

		return LoadBeaconHeader(c.getContext(), c, root)
	})
	if err != nil {
		return
	}

	isNew, err = block.EnsureBlock(func() (*spec.VersionedSignedBeaconBlock, error) {
		t1 := time.Now()
		defer func() {
			processingTimes[0] += time.Since(t1)
		}()

		return LoadBeaconBlock(c.getContext(), c, root)
	})
	if err != nil {
		return
	}

	if loadPayload {
		newPayload, _ := block.EnsureExecutionPayload(func() (*gloas.SignedExecutionPayloadEnvelope, error) {
			t1 := time.Now()
			defer func() {
				processingTimes[0] += time.Since(t1)
			}()

			return LoadExecutionPayload(c.getContext(), c, root)
		})

		if !isNew && newPayload {
			// write payload to db
			err = c.persistExecutionPayload(block)
			if err != nil {
				return
			}
		}
	}

	if slot >= finalizedSlot && isNew {
		c.indexer.blockCache.addBlockToParentMap(block)
		c.indexer.blockCache.addBlockToExecBlockMap(block)

		// Check for cached execution times and add them to the block
		blockIndex := block.GetBlockIndex()
		if blockIndex != nil && !bytes.Equal(blockIndex.ExecutionHash[:], zeroHash[:]) {
			executionHash := common.Hash(blockIndex.ExecutionHash)
			cachedTimes := c.indexer.executionTimeProvider.GetAndDeleteExecutionTimes(executionHash)
			for _, cachedTime := range cachedTimes {
				// Convert the cached time to beacon ExecutionTime format
				client := cachedTime.GetClient()
				execTime := ExecutionTime{
					ClientType: client.GetClientType().Uint8(),
					MinTime:    cachedTime.GetTime(),
					MaxTime:    cachedTime.GetTime(),
					AvgTime:    cachedTime.GetTime(),
					Count:      1,
				}
				block.AddExecutionTime(execTime)
			}
			if len(cachedTimes) > 0 {
				c.logger.WithFields(map[string]interface{}{
					"slot":           block.Slot,
					"execution_hash": executionHash.Hex(),
					"times_count":    len(cachedTimes),
				}).Debug("Added cached execution times to block")
			}
		}

		t1 := time.Now()

		// fork detection
		err2 := c.indexer.forkCache.processBlock(block)
		if err2 != nil {
			c.logger.Warnf("failed processing new fork: %v", err2)
		}

		// insert into unfinalized blocks
		var dbBlock *dbtypes.UnfinalizedBlock
		dbBlock, err = block.buildUnfinalizedBlock(c.indexer.blockCompression)
		if err != nil {
			return
		}

		processingTimes[1] = time.Since(t1)
		t1 = time.Now()

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

		processingTimes[2] = time.Since(t1)

		block.isInUnfinalizedDb = true
		c.indexer.blockCache.latestBlock = block
		c.indexer.blockDispatcher.Fire(block)
	}

	if slot < finalizedSlot && !block.isInFinalizedDb {
		// process new orphaned block in finalized epoch
		// TODO: insert new orphaned block to db

		c.logger.Errorf("new orphaned block in finalized epoch %v: %v [%v] - OPEN TODO", chainState.EpochOfSlot(slot), slot, root.String())
	}

	return
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
			headerRsp, err := LoadBeaconHeader(c.getContext(), c, parentRoot)
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

		var processingTimes []time.Duration
		if parentBlock == nil {
			var err error

			parentBlock, isNewBlock, processingTimes, err = c.processBlock(parentSlot, parentRoot, parentHead, false, true)
			if err != nil {
				return fmt.Errorf("could not process block [0x%x]: %v", parentRoot, err)
			}
		}

		c.emitBlockLogEntry(parentSlot, parentRoot, "backfill", isNewBlock, parentBlock.forkId, processingTimes)

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

// processExecutionPayloadEvent processes an execution payload event from the event stream.
func (c *Client) processExecutionPayloadAvailableEvent(executionPayloadEvent *v1.ExecutionPayloadAvailableEvent) error {
	if c.client.GetStatus() != consensus.ClientStatusOnline && c.client.GetStatus() != consensus.ClientStatusOptimistic {
		// client is not ready, skip
		return nil
	}

	chainState := c.client.GetPool().GetChainState()
	finalizedSlot := chainState.GetFinalizedSlot()

	var block *Block

	if executionPayloadEvent.Slot < finalizedSlot {
		// block is in finalized epoch
		// known block or a new orphaned block

		// don't add to cache, process this block right after loading the details
		block = newBlock(c.indexer.dynSsz, executionPayloadEvent.BlockRoot, executionPayloadEvent.Slot, 0)

		dbBlockHead := db.GetBlockHeadByRoot(executionPayloadEvent.BlockRoot[:])
		if dbBlockHead != nil {
			block.isInFinalizedDb = true
			block.parentRoot = (*phase0.Root)(dbBlockHead.ParentRoot)
		}

	} else {
		block = c.indexer.blockCache.getBlockByRoot(executionPayloadEvent.BlockRoot)
	}

	if block == nil {
		c.logger.Warnf("execution payload event for unknown block %v:%v [0x%x]", chainState.EpochOfSlot(executionPayloadEvent.Slot), executionPayloadEvent.Slot, executionPayloadEvent.BlockRoot)
		return nil
	}

	newPayload, err := block.EnsureExecutionPayload(func() (*gloas.SignedExecutionPayloadEnvelope, error) {
		return LoadExecutionPayload(c.getContext(), c, executionPayloadEvent.BlockRoot)
	})
	if err != nil {
		return err
	}

	if newPayload {
		// write payload to db
		err = c.persistExecutionPayload(block)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) persistExecutionPayload(block *Block) error {
	payloadVer, payloadSSZ, err := MarshalVersionedSignedExecutionPayloadEnvelopeSSZ(block.dynSsz, block.executionPayload, c.indexer.blockCompression)
	if err != nil {
		return fmt.Errorf("marshal execution payload ssz failed: %v", err)
	}

	return db.RunDBTransaction(func(tx *sqlx.Tx) error {
		err := db.UpdateUnfinalizedBlockPayload(block.Root[:], payloadVer, payloadSSZ, tx)
		if err != nil {
			return err
		}

		return nil
	})
}

func (c *Client) processExecutionPayloadBidEvent(executionPayloadBidEvent *gloas.SignedExecutionPayloadBid) error {
	bid := &dbtypes.BlockBid{
		ParentRoot:   executionPayloadBidEvent.Message.ParentBlockRoot[:],
		ParentHash:   executionPayloadBidEvent.Message.ParentBlockHash[:],
		BlockHash:    executionPayloadBidEvent.Message.BlockHash[:],
		FeeRecipient: executionPayloadBidEvent.Message.FeeRecipient[:],
		GasLimit:     uint64(executionPayloadBidEvent.Message.GasLimit),
		BuilderIndex: uint64(executionPayloadBidEvent.Message.BuilderIndex),
		Slot:         uint64(executionPayloadBidEvent.Message.Slot),
		Value:        uint64(executionPayloadBidEvent.Message.Value),
		ElPayment:    uint64(executionPayloadBidEvent.Message.ExecutionPayment),
	}
	c.indexer.blockBidCache.AddBid(bid)
	return nil
}
