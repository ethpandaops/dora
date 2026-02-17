package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/dora/blockdb"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/golang/snappy"
	dynssz "github.com/pk910/dynamic-ssz"
)

func handleSlotDownload(ctx context.Context, w http.ResponseWriter, blockSlot int64, blockRoot []byte, downloadType string) error {
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
	var blockData *services.CombinedBlockResponse
	var err error
	if blockSlot > -1 {
		if phase0.Slot(blockSlot) <= currentSlot {
			blockData, err = services.GlobalBeaconService.GetSlotDetailsBySlot(ctx, phase0.Slot(blockSlot))
		}
	} else {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, phase0.Root(blockRoot))
	}

	if err != nil {
		return fmt.Errorf("error getting block data: %v", err)
	}

	if blockData == nil || blockData.Block == nil {
		return fmt.Errorf("block not found")
	}

	switch downloadType {
	case "block-ssz":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=block-%d-%x.ssz", blockData.Header.Message.Slot, blockData.Root[:]))

		dynSsz := services.GlobalBeaconService.GetBeaconIndexer().GetDynSSZ()
		_, blockSSZ, err := beacon.MarshalVersionedSignedBeaconBlockSSZ(dynSsz, blockData.Block, false, true)
		if err != nil {
			return fmt.Errorf("error serializing block: %v", err)
		}
		w.Write(blockSSZ)
		return nil

	case "block-json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=block-%d-%x.json", blockData.Header.Message.Slot, blockData.Root[:]))

		_, jsonRes, err := beacon.MarshalVersionedSignedBeaconBlockJson(blockData.Block)
		if err != nil {
			return fmt.Errorf("error serializing block: %v", err)
		}
		w.Write(jsonRes)
		return nil

	case "header-ssz":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=header-%d-%x.ssz", blockData.Header.Message.Slot, blockData.Root[:]))
		headerSSZ, err := blockData.Header.MarshalSSZ()
		if err != nil {
			return fmt.Errorf("error serializing header: %v", err)
		}
		w.Write(headerSSZ)
		return nil

	case "header-json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=header-%d-%x.json", blockData.Header.Message.Slot, blockData.Root[:]))
		jsonRes, err := blockData.Header.MarshalJSON()
		if err != nil {
			return fmt.Errorf("error serializing header: %v", err)
		}
		w.Write(jsonRes)
		return nil

	case "receipts-json":
		return handleReceiptsDownload(ctx, w, blockData)

	case "block-body-json":
		return handleBlockBodyDownload(w, blockData)

	default:
		return fmt.Errorf("unknown download type: %s", downloadType)
	}
}

// execBlockJSON matches the eth_getBlockByHash JSON format (with full transactions).
type execBlockJSON struct {
	Number        string            `json:"number"`
	Hash          string            `json:"hash"`
	ParentHash    string            `json:"parentHash"`
	Nonce         string            `json:"nonce"`
	Sha3Uncles    string            `json:"sha3Uncles"`
	LogsBloom     string            `json:"logsBloom"`
	StateRoot     string            `json:"stateRoot"`
	ReceiptsRoot  string            `json:"receiptsRoot"`
	Miner         string            `json:"miner"`
	Difficulty    string            `json:"difficulty"`
	ExtraData     string            `json:"extraData"`
	GasLimit      string            `json:"gasLimit"`
	GasUsed       string            `json:"gasUsed"`
	Timestamp     string            `json:"timestamp"`
	MixHash       string            `json:"mixHash"`
	BaseFeePerGas string            `json:"baseFeePerGas"`
	Transactions  []json.RawMessage `json:"transactions"`
	Uncles        []string          `json:"uncles"`
	Withdrawals   []*withdrawalJSON `json:"withdrawals,omitempty"`
	BlobGasUsed   string            `json:"blobGasUsed,omitempty"`
	ExcessBlobGas string            `json:"excessBlobGas,omitempty"`
}

// withdrawalJSON matches the withdrawal format in eth_getBlockByHash.
type withdrawalJSON struct {
	Index          string `json:"index"`
	ValidatorIndex string `json:"validatorIndex"`
	Address        string `json:"address"`
	Amount         string `json:"amount"`
}

// handleBlockBodyDownload builds and returns the execution block in
// eth_getBlockByHash JSON format, reconstructed from the beacon block's
// execution payload.
func handleBlockBodyDownload(w http.ResponseWriter, blockData *services.CombinedBlockResponse) error {
	executionPayload, err := blockData.Block.ExecutionPayload()
	if err != nil || executionPayload == nil {
		return fmt.Errorf("block has no execution payload")
	}

	blockHash, _ := executionPayload.BlockHash()
	blockNumber, _ := executionPayload.BlockNumber()
	parentHash, _ := executionPayload.ParentHash()
	feeRecipient, _ := executionPayload.FeeRecipient()
	stateRoot, _ := executionPayload.StateRoot()
	receiptsRoot, _ := executionPayload.ReceiptsRoot()
	logsBloom, _ := executionPayload.LogsBloom()
	prevRandao, _ := executionPayload.PrevRandao()
	gasLimit, _ := executionPayload.GasLimit()
	gasUsed, _ := executionPayload.GasUsed()
	timestamp, _ := executionPayload.Timestamp()
	extraData, _ := executionPayload.ExtraData()
	baseFeePerGas, _ := executionPayload.BaseFeePerGas()

	blockHashHex := fmt.Sprintf("0x%x", blockHash[:])
	blockNumberHex := fmt.Sprintf("0x%x", uint64(blockNumber))

	block := &execBlockJSON{
		Number:        blockNumberHex,
		Hash:          blockHashHex,
		ParentHash:    fmt.Sprintf("0x%x", parentHash[:]),
		Nonce:         "0x0000000000000000",
		Sha3Uncles:    "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
		LogsBloom:     fmt.Sprintf("0x%x", logsBloom[:]),
		StateRoot:     fmt.Sprintf("0x%x", stateRoot[:]),
		ReceiptsRoot:  fmt.Sprintf("0x%x", receiptsRoot[:]),
		Miner:         fmt.Sprintf("0x%x", feeRecipient[:]),
		Difficulty:    "0x0",
		ExtraData:     fmt.Sprintf("0x%x", extraData),
		GasLimit:      fmt.Sprintf("0x%x", gasLimit),
		GasUsed:       fmt.Sprintf("0x%x", gasUsed),
		Timestamp:     fmt.Sprintf("0x%x", timestamp),
		MixHash:       fmt.Sprintf("0x%x", prevRandao[:]),
		BaseFeePerGas: fmt.Sprintf("0x%x", baseFeePerGas.ToBig()),
		Uncles:        []string{},
	}

	// Deneb+ blob gas fields.
	if excessBlobGas, err := executionPayload.ExcessBlobGas(); err == nil {
		block.ExcessBlobGas = fmt.Sprintf("0x%x", excessBlobGas)
	}
	if blobGasUsed, err := executionPayload.BlobGasUsed(); err == nil {
		block.BlobGasUsed = fmt.Sprintf("0x%x", blobGasUsed)
	}

	// Decode and serialize transactions.
	transactions, err := executionPayload.Transactions()
	if err != nil {
		return fmt.Errorf("failed to get transactions: %w", err)
	}

	block.Transactions = make([]json.RawMessage, 0, len(transactions))
	for i, txBytes := range transactions {
		var tx ethtypes.Transaction
		if err := tx.UnmarshalBinary(txBytes); err != nil {
			return fmt.Errorf("failed to decode tx %d: %w", i, err)
		}

		// Marshal the tx, then augment with block context fields.
		txJSON, err := tx.MarshalJSON()
		if err != nil {
			return fmt.Errorf("failed to marshal tx %d: %w", i, err)
		}

		var txMap map[string]any
		if err := json.Unmarshal(txJSON, &txMap); err != nil {
			return fmt.Errorf("failed to parse tx json %d: %w", i, err)
		}

		txMap["blockHash"] = blockHashHex
		txMap["blockNumber"] = blockNumberHex
		txMap["transactionIndex"] = fmt.Sprintf("0x%x", i)

		// Recover sender address.
		chainID := tx.ChainId()
		if chainID != nil && chainID.Sign() == 0 {
			chainID = nil
		}
		if from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(chainID), &tx); err == nil {
			txMap["from"] = fmt.Sprintf("0x%x", from[:])
		}

		augmented, err := json.Marshal(txMap)
		if err != nil {
			return fmt.Errorf("failed to re-marshal tx %d: %w", i, err)
		}
		block.Transactions = append(block.Transactions, augmented)
	}

	// Withdrawals (Capella+).
	if withdrawals, err := executionPayload.Withdrawals(); err == nil && len(withdrawals) > 0 {
		block.Withdrawals = make([]*withdrawalJSON, len(withdrawals))
		for i, w := range withdrawals {
			block.Withdrawals[i] = &withdrawalJSON{
				Index:          fmt.Sprintf("0x%x", w.Index),
				ValidatorIndex: fmt.Sprintf("0x%x", w.ValidatorIndex),
				Address:        fmt.Sprintf("0x%x", w.Address[:]),
				Amount:         fmt.Sprintf("0x%x", w.Amount),
			}
		}
	}

	slot := uint64(blockData.Header.Message.Slot)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=block-body-%d-%x.json",
		slot, blockData.Root[:],
	))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(block)
}

// receiptJSON matches the eth_getTransactionReceipt JSON format.
type receiptJSON struct {
	BlockHash         string     `json:"blockHash"`
	BlockNumber       string     `json:"blockNumber"`
	TransactionHash   string     `json:"transactionHash"`
	TransactionIndex  string     `json:"transactionIndex"`
	From              string     `json:"from"`
	To                *string    `json:"to"`
	CumulativeGasUsed string     `json:"cumulativeGasUsed"`
	GasUsed           string     `json:"gasUsed"`
	EffectiveGasPrice string     `json:"effectiveGasPrice"`
	ContractAddress   *string    `json:"contractAddress"`
	Logs              []*logJSON `json:"logs"`
	LogsBloom         string     `json:"logsBloom"`
	Type              string     `json:"type"`
	Status            string     `json:"status"`
	BlobGasUsed       string     `json:"blobGasUsed,omitempty"`
	BlobGasPrice      string     `json:"blobGasPrice,omitempty"`
}

// logJSON matches the log entry format in eth_getTransactionReceipt.
type logJSON struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	BlockNumber      string   `json:"blockNumber"`
	TransactionHash  string   `json:"transactionHash"`
	TransactionIndex string   `json:"transactionIndex"`
	BlockHash        string   `json:"blockHash"`
	LogIndex         string   `json:"logIndex"`
	Removed          bool     `json:"removed"`
}

// handleReceiptsDownload builds and returns JSON receipts for all transactions
// in a block, reconstructed entirely from blockdb execution data.
func handleReceiptsDownload(ctx context.Context, w http.ResponseWriter, blockData *services.CombinedBlockResponse) error {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		return fmt.Errorf("execution data storage not available")
	}

	slot := uint64(blockData.Header.Message.Slot)
	blockRoot := blockData.Root[:]

	executionPayload, err := blockData.Block.ExecutionPayload()
	if err != nil || executionPayload == nil {
		return fmt.Errorf("block has no execution payload")
	}

	blockHash, _ := executionPayload.BlockHash()
	blockNumber, _ := executionPayload.BlockNumber()

	transactions, err := executionPayload.Transactions()
	if err != nil {
		return fmt.Errorf("failed to get transactions: %w", err)
	}

	blockHashHex := fmt.Sprintf("0x%x", blockHash[:])
	blockNumberHex := fmt.Sprintf("0x%x", uint64(blockNumber))

	fullBlob, err := blockdb.GlobalBlockDb.GetExecData(ctx, slot, blockRoot)
	if err != nil {
		return fmt.Errorf("failed to get exec data: %w", err)
	}

	if fullBlob == nil {
		return fmt.Errorf("execution data not available for this block")
	}

	receipts, err := buildReceiptsFromFullBlob(
		fullBlob, transactions,
		blockHashHex, blockNumberHex,
	)
	if err != nil {
		return fmt.Errorf("failed to build receipts: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=receipts-%d-%x.json",
		slot, blockData.Root[:],
	))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	return encoder.Encode(receipts)
}

// buildReceiptsFromFullBlob builds receipts from a full DXTX blob.
// BlobGasPrice is read from the BlockReceiptMeta section.
func buildReceiptsFromFullBlob(
	data []byte,
	transactions []bellatrix.Transaction,
	blockHashHex, blockNumberHex string,
) ([]*receiptJSON, error) {
	obj, err := bdbtypes.ParseExecDataIndex(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse exec data: %w", err)
	}

	txCount := uint32(len(obj.Transactions))

	// Extract block-wide receipt metadata.
	var blobGasPrice uint64

	if obj.BlockMetaCompLen > 0 {
		blockMetaCompressed, err := obj.ExtractBlockMeta(data)
		if err != nil {
			return nil, fmt.Errorf("failed to extract block meta: %w", err)
		}

		blockMetaRaw, err := snappy.Decode(nil, blockMetaCompressed)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress block meta: %w", err)
		}

		ds := dynssz.GetGlobalDynSsz()

		blockMeta := &bdbtypes.BlockReceiptMeta{}
		if err := ds.UnmarshalSSZ(blockMeta, blockMetaRaw); err != nil {
			return nil, fmt.Errorf("failed to decode block meta: %w", err)
		}

		blobGasPrice = blockMeta.BlobGasPrice
	}
	receipts := make([]*receiptJSON, 0, len(transactions))

	for i, txBytes := range transactions {
		var tx ethtypes.Transaction
		if err := tx.UnmarshalBinary(txBytes); err != nil {
			return nil, fmt.Errorf("failed to decode tx %d: %w", i, err)
		}

		txHash := tx.Hash()
		entry := obj.FindTxEntry(txHash[:])
		if entry == nil {
			return nil, fmt.Errorf("tx %d (%s) not found in exec data", i, txHash.Hex())
		}

		if entry.ReceiptMetaCompLen == 0 {
			return nil, fmt.Errorf("tx %d has no receipt metadata", i)
		}

		metaCompressed, err := bdbtypes.ExtractSectionData(data, txCount, entry.ReceiptMetaOffset, entry.ReceiptMetaCompLen)
		if err != nil {
			return nil, fmt.Errorf("failed to extract receipt meta for tx %d: %w", i, err)
		}

		var eventsCompressed []byte
		if entry.EventsCompLen > 0 {
			eventsCompressed, err = bdbtypes.ExtractSectionData(data, txCount, entry.EventsOffset, entry.EventsCompLen)
			if err != nil {
				return nil, fmt.Errorf("failed to extract events for tx %d: %w", i, err)
			}
		}

		receipt, err := buildSingleReceipt(
			txHash[:], uint64(i),
			blockHashHex, blockNumberHex,
			metaCompressed, eventsCompressed, blobGasPrice,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build receipt for tx %d: %w", i, err)
		}

		receipts = append(receipts, receipt)
	}

	return receipts, nil
}

// buildSingleReceipt decodes compressed receiptMeta and events sections and
// assembles a single receipt JSON object.
func buildSingleReceipt(
	txHash []byte, txIndex uint64,
	blockHashHex, blockNumberHex string,
	metaCompressed, eventsCompressed []byte,
	blobGasPrice uint64,
) (*receiptJSON, error) {
	// Decode receipt metadata
	metaRaw, err := snappy.Decode(nil, metaCompressed)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress receipt meta: %w", err)
	}

	var meta bdbtypes.ReceiptMetaData
	if err := dynssz.GetGlobalDynSsz().UnmarshalSSZ(&meta, metaRaw); err != nil {
		return nil, fmt.Errorf("failed to decode receipt meta: %w", err)
	}

	txHashHex := fmt.Sprintf("0x%x", txHash)
	txIndexHex := fmt.Sprintf("0x%x", txIndex)

	receipt := &receiptJSON{
		BlockHash:         blockHashHex,
		BlockNumber:       blockNumberHex,
		TransactionHash:   txHashHex,
		TransactionIndex:  txIndexHex,
		From:              fmt.Sprintf("0x%x", meta.From[:]),
		CumulativeGasUsed: fmt.Sprintf("0x%x", meta.CumulativeGasUsed),
		GasUsed:           fmt.Sprintf("0x%x", meta.GasUsed),
		EffectiveGasPrice: fmt.Sprintf("0x%x", meta.EffectiveGasPrice.ToBig()),
		LogsBloom:         fmt.Sprintf("0x%x", meta.LogsBloom[:]),
		Type:              fmt.Sprintf("0x%x", meta.TxType),
		Status:            fmt.Sprintf("0x%x", meta.Status),
	}

	// To field: null for contract creation
	if meta.HasContractAddr {
		contractAddr := fmt.Sprintf("0x%x", meta.ContractAddress[:])
		receipt.ContractAddress = &contractAddr
	} else {
		toAddr := fmt.Sprintf("0x%x", meta.To[:])
		receipt.To = &toAddr
	}

	// Blob gas fields (EIP-4844)
	if meta.BlobGasUsed > 0 {
		receipt.BlobGasUsed = fmt.Sprintf("0x%x", meta.BlobGasUsed)
		if blobGasPrice > 0 {
			receipt.BlobGasPrice = fmt.Sprintf("0x%x", blobGasPrice)
		}
	}

	// Decode events/logs
	receipt.Logs = make([]*logJSON, 0)

	if len(eventsCompressed) > 0 {
		eventsRaw, err := snappy.Decode(nil, eventsCompressed)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress events: %w", err)
		}

		var events bdbtypes.EventDataList
		if err := dynssz.GetGlobalDynSsz().UnmarshalSSZ(&events, eventsRaw); err != nil {
			return nil, fmt.Errorf("failed to decode events: %w", err)
		}

		receipt.Logs = make([]*logJSON, 0, len(events))

		for j := range events {
			ev := &events[j]
			topics := make([]string, 0, len(ev.Topics))
			for _, topic := range ev.Topics {
				topics = append(topics, fmt.Sprintf("0x%x", topic))
			}

			receipt.Logs = append(receipt.Logs, &logJSON{
				Address:          fmt.Sprintf("0x%x", ev.Source[:]),
				Topics:           topics,
				Data:             fmt.Sprintf("0x%x", ev.Data),
				BlockNumber:      blockNumberHex,
				TransactionHash:  txHashHex,
				TransactionIndex: txIndexHex,
				BlockHash:        blockHashHex,
				LogIndex:         fmt.Sprintf("0x%x", ev.EventIndex),
				Removed:          false,
			})
		}
	}

	return receipt, nil
}
