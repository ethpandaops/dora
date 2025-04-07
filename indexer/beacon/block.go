package beacon

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	dynssz "github.com/pk910/dynamic-ssz"
)

// Block represents a beacon block.
type Block struct {
	Root              phase0.Root
	Slot              phase0.Slot
	dynSsz            *dynssz.DynSsz
	parentRoot        *phase0.Root
	dependentRoot     *phase0.Root
	forkId            ForkKey
	forkChecked       bool
	headerMutex       sync.Mutex
	headerChan        chan bool
	header            *phase0.SignedBeaconBlockHeader
	blockMutex        sync.Mutex
	blockChan         chan bool
	block             *spec.VersionedSignedBeaconBlock
	blockIndex        *BlockBodyIndex
	isInFinalizedDb   bool // block is in finalized table (slots)
	isInUnfinalizedDb bool // block is in unfinalized table (unfinalized_blocks)
	isDisposed        bool // block is disposed
	processingStatus  dbtypes.UnfinalizedBlockStatus
	seenMutex         sync.RWMutex
	seenMap           map[uint16]*Client
	processedActivity uint8
	blockResults      [][]uint8
	blockResultsMutex sync.Mutex
}

// BlockBodyIndex holds important block properties that are used as index for cache lookups.
// this structure should be preserved after pruning, so the block is still identifiable.
type BlockBodyIndex struct {
	Graffiti           [32]byte
	ExecutionExtraData []byte
	ExecutionHash      phase0.Hash32
	ExecutionNumber    uint64
}

// newBlock creates a new Block instance.
func newBlock(dynSsz *dynssz.DynSsz, root phase0.Root, slot phase0.Slot) *Block {
	block := &Block{
		Root:       root,
		Slot:       slot,
		dynSsz:     dynSsz,
		seenMap:    make(map[uint16]*Client),
		headerChan: make(chan bool),
		blockChan:  make(chan bool),
	}

	return block
}

func (block *Block) Dispose() {
	block.isDisposed = true
	block.header = nil
	block.block = nil
	block.blockIndex = nil
	block.seenMap = nil
}

// GetSeenBy returns a list of clients that have seen this block.
func (block *Block) GetSeenBy() []*Client {
	if block.isDisposed {
		return nil
	}

	block.seenMutex.RLock()
	defer block.seenMutex.RUnlock()

	clients := []*Client{}

	for _, client := range block.seenMap {
		clients = append(clients, client)
	}

	rand.Shuffle(len(clients), func(i, j int) {
		clients[i], clients[j] = clients[j], clients[i]
	})

	return clients
}

// SetSeenBy sets the client that has seen this block.
func (block *Block) SetSeenBy(client *Client) {
	if block.isDisposed {
		return
	}

	block.seenMutex.Lock()
	defer block.seenMutex.Unlock()
	block.seenMap[client.index] = client
}

// GetHeader returns the signed beacon block header of this block.
func (block *Block) GetHeader() *phase0.SignedBeaconBlockHeader {
	if block.header != nil {
		return block.header
	}

	return block.header
}

// AwaitHeader waits for the signed beacon block header of this block to be available.
func (block *Block) AwaitHeader(ctx context.Context, timeout time.Duration) *phase0.SignedBeaconBlockHeader {
	if block.isDisposed {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-block.headerChan:
	case <-time.After(timeout):
	case <-ctx.Done():
	}

	return block.header
}

// GetBlock returns the versioned signed beacon block of this block.
func (block *Block) GetBlock() *spec.VersionedSignedBeaconBlock {
	if block.isDisposed {
		return nil
	}

	if block.block != nil {
		return block.block
	}

	if block.isInUnfinalizedDb {
		dbBlock := db.GetUnfinalizedBlock(block.Root[:])
		if dbBlock != nil {
			blockBody, err := UnmarshalVersionedSignedBeaconBlockSSZ(block.dynSsz, dbBlock.BlockVer, dbBlock.BlockSSZ)
			if err == nil {
				return blockBody
			}
		}
	}

	return nil
}

// AwaitBlock waits for the versioned signed beacon block of this block to be available.
func (block *Block) AwaitBlock(ctx context.Context, timeout time.Duration) *spec.VersionedSignedBeaconBlock {
	if block.isDisposed {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-block.blockChan:
	case <-time.After(timeout):
	case <-ctx.Done():
	}

	return block.block
}

// GetParentRoot returns the parent root of this block.
func (block *Block) GetParentRoot() *phase0.Root {
	if block.isDisposed {
		return nil
	}

	if block.parentRoot != nil {
		return block.parentRoot
	}

	if block.header == nil {
		return nil
	}

	return &block.header.Message.ParentRoot
}

// SetHeader sets the signed beacon block header of this block.
func (block *Block) SetHeader(header *phase0.SignedBeaconBlockHeader) {
	if block.isDisposed {
		return
	}

	block.header = header
	if header != nil {
		close(block.headerChan)
	}
}

// EnsureHeader ensures that the signed beacon block header of this block is available.
func (block *Block) EnsureHeader(loadHeader func() (*phase0.SignedBeaconBlockHeader, error)) error {
	if block.isDisposed || block.header != nil {
		return nil
	}

	if block.isInUnfinalizedDb || block.isInFinalizedDb {
		return nil
	}

	block.headerMutex.Lock()
	defer block.headerMutex.Unlock()

	if block.header != nil {
		return nil
	}

	header, err := loadHeader()
	if err != nil {
		return err
	}

	block.header = header
	close(block.headerChan)

	return nil
}

// SetBlock sets the versioned signed beacon block of this block.
func (block *Block) SetBlock(body *spec.VersionedSignedBeaconBlock) {
	if block.isDisposed {
		return
	}

	block.setBlockIndex(body)
	block.block = body

	if block.blockChan != nil {
		close(block.blockChan)
		block.blockChan = nil
	}
}

// EnsureBlock ensures that the versioned signed beacon block of this block is available.
func (block *Block) EnsureBlock(loadBlock func() (*spec.VersionedSignedBeaconBlock, error)) (bool, error) {
	if block.isDisposed || block.block != nil {
		return false, nil
	}

	if block.isInUnfinalizedDb || block.isInFinalizedDb {
		return false, nil
	}

	block.blockMutex.Lock()
	defer block.blockMutex.Unlock()

	if block.block != nil {
		return false, nil
	}

	blockBody, err := loadBlock()
	if err != nil {
		return false, err
	}

	block.setBlockIndex(blockBody)
	block.block = blockBody
	if block.blockChan != nil {
		close(block.blockChan)
		block.blockChan = nil
	}

	return true, nil
}

// setBlockIndex sets the block index of this block.
func (block *Block) setBlockIndex(body *spec.VersionedSignedBeaconBlock) {
	blockIndex := &BlockBodyIndex{}
	blockIndex.Graffiti, _ = body.Graffiti()
	blockIndex.ExecutionExtraData, _ = getBlockExecutionExtraData(body)
	blockIndex.ExecutionHash, _ = body.ExecutionBlockHash()
	blockIndex.ExecutionNumber, _ = body.ExecutionBlockNumber()

	block.blockIndex = blockIndex
}

// GetBlockIndex returns the block index of this block.
func (block *Block) GetBlockIndex() *BlockBodyIndex {
	if block.isDisposed {
		return nil
	}

	if block.blockIndex != nil {
		return block.blockIndex
	}

	blockBody := block.GetBlock()
	if blockBody != nil {
		block.setBlockIndex(blockBody)
	}

	return block.blockIndex
}

// buildUnfinalizedBlock builds an unfinalized block from the block data.
func (block *Block) buildUnfinalizedBlock(compress bool) (*dbtypes.UnfinalizedBlock, error) {
	if block.isDisposed {
		return nil, fmt.Errorf("block is disposed")
	}

	headerSSZ, err := block.header.MarshalSSZ()
	if err != nil {
		return nil, fmt.Errorf("marshal header ssz failed: %v", err)
	}

	blockVer, blockSSZ, err := MarshalVersionedSignedBeaconBlockSSZ(block.dynSsz, block.GetBlock(), compress, false)
	if err != nil {
		return nil, fmt.Errorf("marshal block ssz failed: %v", err)
	}

	return &dbtypes.UnfinalizedBlock{
		Root:      block.Root[:],
		Slot:      uint64(block.Slot),
		HeaderVer: 1,
		HeaderSSZ: headerSSZ,
		BlockVer:  blockVer,
		BlockSSZ:  blockSSZ,
		Status:    0,
		ForkId:    uint64(block.forkId),
	}, nil
}

// buildOrphanedBlock builds an orphaned block from the block data.
func (block *Block) buildOrphanedBlock(compress bool) (*dbtypes.OrphanedBlock, error) {
	if block.isDisposed {
		return nil, fmt.Errorf("block is disposed")
	}

	headerSSZ, err := block.header.MarshalSSZ()
	if err != nil {
		return nil, fmt.Errorf("marshal header ssz failed: %v", err)
	}

	blockVer, blockSSZ, err := MarshalVersionedSignedBeaconBlockSSZ(block.dynSsz, block.GetBlock(), compress, false)
	if err != nil {
		return nil, fmt.Errorf("marshal block ssz failed: %v", err)
	}

	return &dbtypes.OrphanedBlock{
		Root:      block.Root[:],
		HeaderVer: 1,
		HeaderSSZ: headerSSZ,
		BlockVer:  blockVer,
		BlockSSZ:  blockSSZ,
	}, nil
}

func (block *Block) writeToBlockDb() error {
	if block.isDisposed || block.header == nil || block.block == nil || blockdb.GlobalBlockDb == nil {
		return nil
	}

	_, err := blockdb.GlobalBlockDb.AddBlockWithCallback(context.Background(), uint64(block.Slot), block.Root[:], func() (*btypes.BlockData, error) {
		headerSSZ, err := block.header.MarshalSSZ()
		if err != nil {
			return nil, fmt.Errorf("marshal header ssz failed: %v", err)
		}

		version, ssz, err := MarshalVersionedSignedBeaconBlockSSZ(block.dynSsz, block.block, true, false)
		if err != nil {
			return nil, fmt.Errorf("error marshalling block %v: %v", block.Root.String(), err)
		}

		return &btypes.BlockData{
			HeaderVersion: 1,
			HeaderData:    headerSSZ,
			BodyVersion:   version,
			BodyData:      ssz,
		}, nil
	})
	if err != nil {
		return fmt.Errorf("error adding block %v to blockdb: %v", block.Root.String(), err)
	}

	return nil
}

// unpruneBlockBody retrieves the block body from the database if it is not already present.
func (block *Block) unpruneBlockBody() {
	if block.isDisposed || block.block != nil || !block.isInUnfinalizedDb {
		return
	}

	dbBlock := db.GetUnfinalizedBlock(block.Root[:])
	if dbBlock != nil {
		block.block, _ = UnmarshalVersionedSignedBeaconBlockSSZ(block.dynSsz, dbBlock.BlockVer, dbBlock.BlockSSZ)
	}
}

// GetDbBlock returns the database representation of this block.
func (block *Block) GetDbBlock(indexer *Indexer, isCanonical bool) *dbtypes.Slot {
	if block.isDisposed {
		return nil
	}

	var epochStats *EpochStats
	chainState := indexer.consensusPool.GetChainState()
	if dependentBlock := indexer.blockCache.getDependentBlock(chainState, block, nil); dependentBlock != nil {
		epochStats = indexer.epochCache.getEpochStats(chainState.EpochOfSlot(block.Slot), dependentBlock.Root)
	}

	dbBlock := indexer.dbWriter.buildDbBlock(block, epochStats, nil)
	if dbBlock == nil {
		return nil
	}

	if !isCanonical {
		dbBlock.Status = dbtypes.Orphaned
	}

	return dbBlock
}

// GetDbDeposits returns the database representation of the deposits in this block.
func (block *Block) GetDbDeposits(indexer *Indexer, depositIndex *uint64, isCanonical bool) []*dbtypes.Deposit {
	if block.isDisposed {
		return nil
	}

	dbDeposits := indexer.dbWriter.buildDbDeposits(block, depositIndex, !isCanonical, nil)
	dbDeposits = append(dbDeposits, indexer.dbWriter.buildDbDepositRequests(block, !isCanonical, nil)...)

	return dbDeposits
}

// GetDbVoluntaryExits returns the database representation of the voluntary exits in this block.
func (block *Block) GetDbVoluntaryExits(indexer *Indexer, isCanonical bool) []*dbtypes.VoluntaryExit {
	if block.isDisposed {
		return nil
	}

	return indexer.dbWriter.buildDbVoluntaryExits(block, !isCanonical, nil)
}

// GetDbSlashings returns the database representation of the slashings in this block.
func (block *Block) GetDbSlashings(indexer *Indexer, isCanonical bool) []*dbtypes.Slashing {
	if block.isDisposed {
		return nil
	}

	return indexer.dbWriter.buildDbSlashings(block, !isCanonical, nil)
}

// GetDbWithdrawalRequests returns the database representation of the withdrawal requests in this block.
func (block *Block) GetDbWithdrawalRequests(indexer *Indexer, isCanonical bool) []*dbtypes.WithdrawalRequest {
	if block.isDisposed {
		return nil
	}

	return indexer.dbWriter.buildDbWithdrawalRequests(block, !isCanonical, nil, nil)
}

// GetDbConsolidationRequests returns the database representation of the consolidation requests in this block.
func (block *Block) GetDbConsolidationRequests(indexer *Indexer, isCanonical bool) []*dbtypes.ConsolidationRequest {
	if block.isDisposed {
		return nil
	}

	return indexer.dbWriter.buildDbConsolidationRequests(block, !isCanonical, nil, nil)
}

// GetForkId returns the fork ID of this block.
func (block *Block) GetForkId() ForkKey {
	return block.forkId
}
