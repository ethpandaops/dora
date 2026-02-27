package system_contracts

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jmoiron/sqlx"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/tree"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/indexer/execution"
	"github.com/ethpandaops/dora/utils"
)

const depositContractAbi = `[{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"bytes","name":"pubkey","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"amount","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"signature","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"index","type":"bytes"}],"name":"DepositEvent","type":"event"},{"inputs":[{"internalType":"bytes","name":"pubkey","type":"bytes"},{"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"internalType":"bytes","name":"signature","type":"bytes"},{"internalType":"bytes32","name":"deposit_data_root","type":"bytes32"}],"name":"deposit","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"get_deposit_count","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"get_deposit_root","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes4","name":"interfaceId","type":"bytes4"}],"name":"supportsInterface","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"pure","type":"function"}]`

// DepositIndexer is the indexer for the deposit contract
type DepositIndexer struct {
	indexerCtx *execution.IndexerCtx
	logger     logrus.FieldLogger
	indexer    *contractIndexer[dbtypes.DepositTx]

	depositContractAbi *abi.ABI
	depositEventTopic  []byte
	depositSigDomain   zrnt_common.BLSDomain
}

// NewDepositIndexer creates a new deposit contract indexer
func NewDepositIndexer(indexer *execution.IndexerCtx) *DepositIndexer {
	batchSize := utils.Config.ExecutionApi.LogBatchSize
	if batchSize == 0 {
		batchSize = 1000
	}

	contractAbi, err := abi.JSON(strings.NewReader(depositContractAbi))
	if err != nil {
		log.Fatal(err)
	}

	depositEventTopic := crypto.Keccak256Hash([]byte(contractAbi.Events["DepositEvent"].Sig))

	specs := indexer.ChainState.GetSpecs()
	genesisForkVersion := specs.GenesisForkVersion
	depositSigDomain := zrnt_common.ComputeDomain(zrnt_common.DOMAIN_DEPOSIT, zrnt_common.Version(genesisForkVersion), zrnt_common.Root{})

	ds := &DepositIndexer{
		indexerCtx:         indexer,
		logger:             indexer.Logger.WithField("indexer", "deposit"),
		depositContractAbi: &contractAbi,
		depositEventTopic:  depositEventTopic[:],
		depositSigDomain:   depositSigDomain,
	}

	// create contract indexer for the deposit contract
	ds.indexer = newContractIndexer(
		indexer,
		ds.logger.WithField("routine", "crawler"),
		&contractIndexerOptions[dbtypes.DepositTx]{
			stateKey:        "indexer.depositstate",
			batchSize:       batchSize,
			contractAddress: common.Address(specs.DepositContractAddress),
			deployBlock:     uint64(utils.Config.ExecutionApi.DepositDeployBlock),
			dequeueRate:     0,

			processFinalTx:  ds.processFinalTx,
			processRecentTx: ds.processRecentTx,
			persistTxs:      ds.persistDepositTxs,
		},
	)

	go ds.runDepositIndexerLoop()

	return ds
}

// runDepositIndexerLoop is the main loop for the deposit indexer
func (ds *DepositIndexer) runDepositIndexerLoop() {
	defer utils.HandleSubroutinePanic("DepositIndexer.runDepositIndexerLoop", ds.runDepositIndexerLoop)

	for {
		time.Sleep(60 * time.Second)
		ds.logger.Debugf("run deposit indexer logic")

		err := ds.indexer.runContractIndexer()
		if err != nil {
			ds.logger.Errorf("deposit indexer error: %v", err)
		}
	}
}

// processFinalTx is the callback for the contract indexer to process final transactions
// it parses the transaction and returns the corresponding deposit transaction
func (ci *DepositIndexer) processFinalTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, parentTxs []*dbtypes.DepositTx) (*dbtypes.DepositTx, error) {
	requestTx := ci.parseDepositLog(log, parentTxs, 0)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid deposit log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]

	return requestTx, nil
}

// processRecentTx is the callback for the contract indexer to process recent transactions
// it parses the transaction and returns the corresponding deposit transaction
func (ci *DepositIndexer) processRecentTx(log *types.Log, tx *types.Transaction, header *types.Header, txFrom common.Address, dequeueBlock uint64, fork *execution.ForkWithClients, parentTxs []*dbtypes.DepositTx) (*dbtypes.DepositTx, error) {
	forkId := uint64(fork.ForkId)
	clBlock := ci.indexerCtx.BeaconIndexer.GetBlocksByExecutionBlockHash(phase0.Hash32(log.BlockHash))
	if len(clBlock) > 0 {
		forkId = uint64(clBlock[0].GetForkId())
	}

	requestTx := ci.parseDepositLog(log, parentTxs, forkId)
	if requestTx == nil {
		return nil, fmt.Errorf("invalid deposit log")
	}

	txTo := *tx.To()

	requestTx.BlockTime = header.Time
	requestTx.TxSender = txFrom[:]
	requestTx.TxTarget = txTo[:]
	requestTx.ForkId = forkId

	return requestTx, nil
}

// parseDepositLog parses a deposit log and returns the corresponding deposit transaction
func (ci *DepositIndexer) parseDepositLog(log *types.Log, parentTxs []*dbtypes.DepositTx, forkId uint64) *dbtypes.DepositTx {
	if !bytes.Equal(log.Topics[0][:], ci.depositEventTopic) {
		return nil
	}

	event, err := ci.depositContractAbi.Unpack("DepositEvent", log.Data)
	if err != nil {
		ci.logger.Errorf("error decoding deposit event (%v): %v", log.TxHash, err)
		return nil
	}

	requestTx := &dbtypes.DepositTx{
		Index:                 binary.LittleEndian.Uint64(event[4].([]byte)),
		BlockNumber:           log.BlockNumber,
		BlockRoot:             log.BlockHash[:],
		PublicKey:             event[0].([]byte),
		WithdrawalCredentials: event[1].([]byte),
		Amount:                binary.LittleEndian.Uint64(event[2].([]byte)),
		Signature:             event[3].([]byte),
		TxHash:                log.TxHash[:],
	}
	ci.checkDepositValidity(requestTx, parentTxs, forkId)

	return requestTx
}

// persistDepositTxs is the callback for the contract indexer to persist deposit transactions to the database
func (ci *DepositIndexer) persistDepositTxs(tx *sqlx.Tx, requests []*dbtypes.DepositTx) error {
	requestCount := len(requests)
	for requestIdx := 0; requestIdx < requestCount; requestIdx += 500 {
		endIdx := requestIdx + 500
		if endIdx > requestCount {
			endIdx = requestCount
		}

		err := db.InsertDepositTxs(ci.indexerCtx.Ctx, tx, requests[requestIdx:endIdx])
		if err != nil {
			return fmt.Errorf("error while inserting deposit txs: %v", err)
		}
	}

	return nil
}

// checkDepositValidity checks if a deposit transaction has a valid signature
func (ds *DepositIndexer) checkDepositValidity(depositTx *dbtypes.DepositTx, parentTxs []*dbtypes.DepositTx, forkId uint64) {
	// first, check if there is already a valid deposit for the same public key
	isTopUp := false
	for _, parentTx := range parentTxs {
		if bytes.Equal(parentTx.PublicKey, depositTx.PublicKey) && parentTx.ValidSignature == 1 {
			isTopUp = true
			break
		}
	}

	if !isTopUp {
		validator, isFound := ds.indexerCtx.BeaconIndexer.GetValidatorIndexByPubkey(phase0.BLSPubKey(depositTx.PublicKey))
		if isFound {
			validator := ds.indexerCtx.BeaconIndexer.GetValidatorByIndex(validator, nil)
			if validator != nil && validator.ActivationEpoch == 0 {
				// genesis validator won't have a successful deposit, these are always top-ups
				isTopUp = true
			}
		}
	}

	if !isTopUp {
		parentForkIds := ds.indexerCtx.BeaconIndexer.GetParentForkIds(beacon.ForkKey(forkId))
		parentForkIdsUint := make([]uint64, len(parentForkIds))
		for i, forkId := range parentForkIds {
			parentForkIdsUint[i] = uint64(forkId)
		}

		dbDepositTxs, _, err := db.GetDepositTxsFiltered(ds.indexerCtx.Ctx, 0, 1, parentForkIdsUint, &dbtypes.DepositTxFilter{
			PublicKey:    depositTx.PublicKey,
			WithValid:    0,
			WithOrphaned: 0,
		})
		if err == nil && len(dbDepositTxs) > 0 {
			isTopUp = true
		}
	}

	if isTopUp {
		// if there is already a valid deposit for the same public key, set valid signature to 2, indicating that this is a top-up deposit
		depositTx.ValidSignature = 2
		return
	}

	depositMsg := &zrnt_common.DepositMessage{
		Pubkey:                zrnt_common.BLSPubkey(depositTx.PublicKey),
		WithdrawalCredentials: tree.Root(depositTx.WithdrawalCredentials),
		Amount:                zrnt_common.Gwei(depositTx.Amount),
	}
	depositRoot := depositMsg.HashTreeRoot(tree.GetHashFn())
	signingRoot := zrnt_common.ComputeSigningRoot(
		depositRoot,
		ds.depositSigDomain,
	)

	pubkey, err := depositMsg.Pubkey.Pubkey()
	sigData := zrnt_common.BLSSignature(depositTx.Signature)
	sig, err2 := sigData.Signature()
	if err == nil && err2 == nil && blsu.Verify(pubkey, signingRoot[:], sig) {
		depositTx.ValidSignature = 1
	}
}
