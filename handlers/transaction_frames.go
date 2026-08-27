package handlers

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/blockdb"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/golang/snappy"
)

// frameModeNames names the caller each frame mode runs under.
var frameModeNames = map[uint8]string{
	uint8(txtypes.FrameModeDefault): "Default",
	uint8(txtypes.FrameModeVerify):  "Verify",
	uint8(txtypes.FrameModeSender):  "Sender",
}

// frameSpeciesNames turn the mempool rules' species names into readable ones.
var frameSpeciesNames = map[txtypes.FrameSpecies]string{
	txtypes.SpeciesSelfVerify:   "Self verify",
	txtypes.SpeciesOnlyVerify:   "Verify",
	txtypes.SpeciesPay:          "Paymaster",
	txtypes.SpeciesExpiryVerify: "Expiry check",
	txtypes.SpeciesDeploy:       "Account deploy",
	txtypes.SpeciesUserOp:       "User operation",
	txtypes.SpeciesPostOp:       "Settlement",
	txtypes.SpeciesOther:        "Other",
}

// frameStatusText renders a frame's result. Skipped is neither a success nor a failure -
// an earlier frame in its atomic batch failed and this one never ran - and a frame the
// client reported no result for is neither either.
func frameStatusText(status uint8, rolledBack bool) string {
	switch status {
	case dbtypes.ElFrameStatusSuccess:
		if rolledBack {
			return "Rolled back"
		}

		return "Success"
	case dbtypes.ElFrameStatusFailed:
		return "Failed"
	case dbtypes.ElFrameStatusSkipped:
		return "Skipped"
	default:
		return "Unknown"
	}
}

// loadTransactionFrames populates the frames of a frame transaction from its relational
// rows, and labels the transaction by the shape of its validation prefix.
func loadTransactionFrames(ctx context.Context, pageData *models.TransactionPageData, txUid uint64) {
	rows, err := db.GetElTxFramesByTxUid(ctx, txUid)
	if err != nil || len(rows) == 0 {
		return
	}

	// Resolve the frame targets to addresses in one lookup.
	accountIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		if row.ToID != 0 {
			accountIDs = append(accountIDs, row.ToID)
		}
	}

	accounts := make(map[uint64]*dbtypes.ElAccount, len(accountIDs))
	if len(accountIDs) > 0 {
		if resolved, err := db.GetElAccountsByIDs(ctx, accountIDs); err == nil {
			for _, account := range resolved {
				accounts[account.ID] = account
			}
		}
	}

	sender := common.BytesToAddress(pageData.FromAddr)
	frames := make([]*models.TransactionPageDataFrame, 0, len(rows))

	// The frames are rebuilt as their protocol form so the species and validation prefix
	// are read off the same rules the mempool applies, rather than restated here.
	protocolTx := &txtypes.FrameTx{
		Sender: sender,
		Frames: make([]*txtypes.Frame, 0, len(rows)),
	}

	for _, row := range rows {
		frame := &models.TransactionPageDataFrame{
			Index:             uint32(row.FrameIndex),
			Mode:              row.Mode,
			ModeName:          frameModeNames[row.Mode],
			Flags:             row.Flags,
			ApprovesPayment:   row.Flags&txtypes.ApprovePayment != 0,
			ApprovesExecution: row.Flags&txtypes.ApproveExecution != 0,
			AtomicBatch:       row.Flags&txtypes.AtomicBatchFlag != 0,
			Amount:            row.Amount,
			DataLen:           row.DataLen,
			MethodID:          row.MethodID,
			Status:            row.Status,
			StatusText:        frameStatusText(row.Status, row.RolledBack),
			RolledBack:        row.RolledBack,
			ExecGasLimit:      row.ExecGasLimit,
			StateGasLimit:     row.StateGasLimit,
			ExecGasUsed:       row.ExecGasUsed,
			StateGasUsed:      row.StateGasUsed,
			LogCount:          row.LogCount,
		}

		var target *common.Address

		if account, ok := accounts[row.ToID]; ok {
			frame.TargetAddr = account.Address
			frame.HasTarget = true
			frame.TargetIsSender = common.BytesToAddress(account.Address) == sender

			addr := common.BytesToAddress(account.Address)
			target = &addr
		}

		if frame.ModeName == "" {
			frame.ModeName = "Unknown"
		}

		frames = append(frames, frame)
		protocolTx.Frames = append(protocolTx.Frames, &txtypes.Frame{
			Mode:   txtypes.FrameMode(row.Mode),
			Flags:  row.Flags,
			Target: target,
		})
	}

	for i, frame := range frames {
		frame.Species = frameSpeciesNames[protocolTx.Frames[i].Species(sender)]
	}

	assignFrameBatches(frames)

	pageData.Frames = frames
	pageData.FrameCount = uint64(len(frames))
	pageData.FrameShape = frameShapeLabel(protocolTx)
}

// buildFramesFromEnvelope builds the frame list from the transaction itself, for the paths
// that have no relational rows to read from: a transaction reconstructed from blockdb
// after its rows were pruned, or one fetched straight from a client.
//
// The results each frame produced are not in the envelope and are overlaid separately.
func buildFramesFromEnvelope(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	frames := make([]*models.TransactionPageDataFrame, 0, len(frameTx.Frames))

	for i, protocolFrame := range frameTx.Frames {
		target := protocolFrame.ResolvedTarget(frameTx.Sender)

		frame := &models.TransactionPageDataFrame{
			Index:             uint32(i),
			Mode:              uint8(protocolFrame.Mode),
			ModeName:          frameModeNames[uint8(protocolFrame.Mode)],
			Species:           frameSpeciesNames[protocolFrame.Species(frameTx.Sender)],
			Flags:             protocolFrame.Flags,
			ApprovesPayment:   protocolFrame.Flags&txtypes.ApprovePayment != 0,
			ApprovesExecution: protocolFrame.Flags&txtypes.ApproveExecution != 0,
			AtomicBatch:       protocolFrame.IsAtomicBatch(),
			TargetAddr:        target.Bytes(),
			HasTarget:         true,
			TargetIsSender:    target == frameTx.Sender,
			DataLen:           uint32(len(protocolFrame.Data)),
			ExecGasLimit:      protocolFrame.Limits.Execution,
			StateGasLimit:     protocolFrame.Limits.State,
			Status:            dbtypes.ElFrameStatusUnknown,
			StatusText:        frameStatusText(dbtypes.ElFrameStatusUnknown, false),
		}

		if frame.ModeName == "" {
			frame.ModeName = "Unknown"
		}

		if protocolFrame.Value != nil {
			frame.Amount = weiToEth(protocolFrame.Value.ToBig())
		}

		if len(protocolFrame.Data) >= 4 {
			frame.MethodID = protocolFrame.Data[:4]
		}

		frames = append(frames, frame)
	}

	assignFrameBatches(frames)

	pageData.Frames = frames
	pageData.FrameCount = uint64(len(frames))
	pageData.FrameShape = frameShapeLabel(frameTx)
}

// applyFrameResults overlays the result a receipt reports for each frame.
func applyFrameResults(pageData *models.TransactionPageData, results []bdbtypes.FrameReceiptEntry) {
	for i, result := range results {
		if i >= len(pageData.Frames) {
			break
		}

		frame := pageData.Frames[i]
		frame.Status = result.Status
		frame.StatusText = frameStatusText(result.Status, frame.RolledBack)
		frame.ExecGasUsed = result.ExecGasUsed
		frame.StateGasUsed = result.StateGasUsed

		logCount := result.LogCount
		if logCount > 0xffff {
			logCount = 0xffff
		}

		frame.LogCount = uint16(logCount)
	}

	markRolledBackFrames(pageData.Frames)
}

// applyFrameReceiptExtra overlays the frame content a client's receipt reports.
func applyFrameReceiptExtra(pageData *models.TransactionPageData, receipt *txtypes.Receipt) {
	extra := receipt.FrameExtra()
	if extra == nil {
		return
	}

	applyFramePayer(pageData, &bdbtypes.FrameReceiptData{Payer: extra.Payer})

	results := make([]bdbtypes.FrameReceiptEntry, 0, len(extra.Frames))
	for _, frame := range extra.Frames {
		results = append(results, bdbtypes.FrameReceiptEntry{
			Status:       uint8(frame.Status),
			ExecGasUsed:  frame.ExecutionGas,
			StateGasUsed: frame.StateGas,
			LogCount:     uint32(len(frame.Logs)),
		})
	}

	applyFrameResults(pageData, results)
}

// markRolledBackFrames flags the frames of every atomic batch that failed.
//
// A frame that ran before the failure keeps the success status it earned, but the batch
// took its state changes back with it, so showing a plain success would say it did
// something it did not.
func markRolledBackFrames(frames []*models.TransactionPageDataFrame) {
	batchStart := 0

	for i, frame := range frames {
		if frame.AtomicBatch && i+1 < len(frames) {
			continue
		}

		batch := frames[batchStart : i+1]
		batchStart = i + 1

		failed := false

		for _, member := range batch {
			if member.Status == dbtypes.ElFrameStatusFailed {
				failed = true

				break
			}
		}

		if !failed {
			continue
		}

		for _, member := range batch {
			member.RolledBack = true
			member.StatusText = frameStatusText(member.Status, true)
		}
	}
}

// weiToEth converts a wei amount to ether.
func weiToEth(amount *big.Int) float64 {
	if amount == nil {
		return 0
	}

	value, _ := new(big.Float).Quo(new(big.Float).SetInt(amount), big.NewFloat(1e18)).Float64()

	return value
}

// assignFrameBatches groups the frames into atomic batches so they can be shown together.
//
// A batch is a maximal run of frames in which every frame but the last carries the batch
// flag. Frames outside one each form a group of their own.
func assignFrameBatches(frames []*models.TransactionPageDataFrame) {
	batchStart := 0
	batchIndex := 0

	for i, frame := range frames {
		if frame.AtomicBatch && i+1 < len(frames) {
			continue
		}

		size := i + 1 - batchStart
		for _, member := range frames[batchStart : i+1] {
			member.BatchIndex = batchIndex
			member.BatchSize = size
		}

		batchStart = i + 1
		batchIndex++
	}
}

// frameShapeLabel names a frame transaction by its validation prefix, which is the
// shortest run of leading frames whose success settles who pays.
//
// Only four prefixes propagate on the public mempool, and naming them is the single most
// useful thing to say about a frame transaction. Anything else is left unnamed rather
// than guessed at.
func frameShapeLabel(tx *txtypes.FrameTx) string {
	prefixLen := tx.ValidationPrefixLength()
	if prefixLen == 0 {
		return ""
	}

	species := make([]txtypes.FrameSpecies, 0, prefixLen)

	for i := 0; i < prefixLen; i++ {
		found := tx.Frames[i].Species(tx.Sender)

		// An expiry check may lead any of the shapes and is not part of them.
		if found == txtypes.SpeciesExpiryVerify {
			continue
		}

		species = append(species, found)
	}

	deploys := false
	sponsored := false
	selfRelayed := false

	for _, s := range species {
		switch s {
		case txtypes.SpeciesDeploy:
			deploys = true
		case txtypes.SpeciesPay:
			sponsored = true
		case txtypes.SpeciesSelfVerify:
			selfRelayed = true
		}
	}

	switch {
	case deploys && sponsored:
		return "Account deployment, sponsored"
	case deploys:
		return "Account deployment"
	case sponsored:
		return "Sponsored"
	case selfRelayed:
		return "Self-relayed"
	default:
		return ""
	}
}

// applyFrameTxEnvelope fills in what only the transaction envelope carries: whether its
// nonce is an account nonce, and the deadline of an expiry check.
//
// The frames' calldata is not stored relationally, so the deadline is only available
// while the block the transaction came in is still retained.
func applyFrameTxEnvelope(pageData *models.TransactionPageData, frameTx *txtypes.FrameTx) {
	pageData.IsFrameTx = true
	pageData.NonceIsAccount = frameTx.UsesLegacyNonce()

	if !pageData.NonceIsAccount {
		pageData.NonceKeys = make([]string, 0, len(frameTx.NonceKeys))
		for _, key := range frameTx.NonceKeys {
			if key == nil {
				continue
			}

			pageData.NonceKeys = append(pageData.NonceKeys, key.Dec())
		}
	}

	for i, frame := range frameTx.Frames {
		deadline, ok := frame.ExpiryDeadline()
		if !ok {
			continue
		}

		pageData.HasExpiry = true
		pageData.ExpiryTime = time.Unix(int64(deadline), 0)

		if i < len(pageData.Frames) {
			pageData.Frames[i].HasExpiry = true
			pageData.Frames[i].ExpiryTime = pageData.ExpiryTime
		}

		break
	}
}

// loadFramePayerFromBlockdb reads the account that settled a frame transaction's fee.
//
// It is the one thing a frame transaction's page needs that no relational row holds: the
// payer is per-transaction rather than per-frame, and giving el_transactions a column for
// it would cost every ordinary transaction the space too.
func loadFramePayerFromBlockdb(
	ctx context.Context,
	pageData *models.TransactionPageData,
	slot uint64,
	blockRoot []byte,
	txHash []byte,
) {
	frameData := readFrameReceiptFromBlockdb(ctx, slot, blockRoot, txHash)
	if frameData == nil {
		return
	}

	applyFramePayer(pageData, frameData)
}

// applyFramePayer records the payer, and whether it differs from the sender.
func applyFramePayer(pageData *models.TransactionPageData, frameData *bdbtypes.FrameReceiptData) {
	payer := common.Address(frameData.Payer)
	if payer == (common.Address{}) {
		return
	}

	pageData.PayerAddr = payer.Bytes()
	pageData.PayerIsSender = common.BytesToAddress(pageData.FromAddr) == payer
}

// readFrameReceiptFromBlockdb fetches the frame content of a receipt, or nil when the
// transaction has none or the object is gone.
func readFrameReceiptFromBlockdb(
	ctx context.Context,
	slot uint64,
	blockRoot []byte,
	txHash []byte,
) *bdbtypes.FrameReceiptData {
	if blockdb.GlobalBlockDb == nil || !blockdb.GlobalBlockDb.SupportsExecData() {
		return nil
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	sections, err := blockdb.GlobalBlockDb.GetExecDataTxSections(rctx, slot, blockRoot, txHash, bdbtypes.ExecDataSectionReceiptMeta)
	if err != nil || sections == nil || sections.ReceiptMetaData == nil {
		return nil
	}

	metaRaw, err := snappy.Decode(nil, sections.ReceiptMetaData)
	if err != nil {
		return nil
	}

	_, frameData, err := bdbtypes.DecodeReceiptMetaSection(metaRaw)
	if err != nil {
		return nil
	}

	return frameData
}
