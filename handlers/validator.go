package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
)

// Validator will return the main "validator" page using a go template
func Validator(w http.ResponseWriter, r *http.Request) {
	var validatorTemplateFiles = append(layoutTemplateFiles,
		"validator/validator.html",
		"validator/recentBlocks.html",
		"validator/recentAttestations.html",
		"validator/recentDeposits.html",
		"validator/withdrawalRequests.html",
		"validator/consolidationRequests.html",
		"validator/withdrawals.html",
		"validator/txDetails.html",
		"_svg/timeline.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"validator/notfound.html",
	)

	var pageTemplate = templates.GetTemplate(validatorTemplateFiles...)
	data := InitPageData(w, r, "validators", "/validator", "Validator", validatorTemplateFiles)

	var validator *v1.Validator

	vars := mux.Vars(r)
	idxOrPubKey := strings.Replace(vars["idxOrPubKey"], "0x", "", -1)
	validatorPubKey, err := hex.DecodeString(idxOrPubKey)
	if err != nil || len(validatorPubKey) != 48 {
		// search by index^
		validatorIndex, err := strconv.ParseUint(vars["idxOrPubKey"], 10, 64)
		if err == nil {
			validator = services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), false)
		}
	} else {
		// search by pubkey
		validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(validatorPubKey))
		if found {
			validator = services.GlobalBeaconService.GetValidatorByIndex(validatorIndex, false)
		}
	}

	if validator == nil {
		data := InitPageData(w, r, "blockchain", "/validator", "Validator not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "validator.go", "Validator", "", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	tabView := "blocks"
	if r.URL.Query().Has("v") {
		tabView = r.URL.Query().Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		pageData, err := getValidatorPageData(uint64(validator.Index), tabView)
		data.Data = pageData
		pageError = err
	}
	if data.Data == nil || data.Data.(*models.ValidatorPageData) == nil {
		pageError = errors.New("validator not found")
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "validators.go", "Validators", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "validators.go", "Validators", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getValidatorPageData(validatorIndex uint64, tabView string) (*models.ValidatorPageData, error) {
	pageData := &models.ValidatorPageData{}
	pageCacheKey := fmt.Sprintf("validator:%v:%v", validatorIndex, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorPageData(pageCall.CallCtx, validatorIndex, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildValidatorPageData(ctx context.Context, validatorIndex uint64, tabView string) (*models.ValidatorPageData, time.Duration) {
	logrus.Debugf("validator page called: %v", validatorIndex)

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), true)
	if validator == nil {
		return nil, 0
	}

	pageData := &models.ValidatorPageData{
		CurrentEpoch:        uint64(chainState.CurrentEpoch()),
		Index:               uint64(validator.Index),
		Name:                services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
		PublicKey:           validator.Validator.PublicKey[:],
		Balance:             uint64(validator.Balance),
		EffectiveBalance:    uint64(validator.Validator.EffectiveBalance),
		BeaconState:         validator.Status.String(),
		WithdrawCredentials: validator.Validator.WithdrawalCredentials,
		TabView:             tabView,
		ElectraIsActive:     specs.ElectraForkEpoch != nil && uint64(chainState.CurrentEpoch()) >= *specs.ElectraForkEpoch,
	}

	// Check for queued deposits
	filteredQueue := services.GlobalBeaconService.GetFilteredQueuedDeposits(ctx, &services.QueuedDepositFilter{
		PublicKey: validator.Validator.PublicKey[:],
		NoIndex:   true,
	})
	pageData.QueuedDepositCount = uint64(len(filteredQueue))

	if strings.HasPrefix(validator.Status.String(), "pending") {
		pageData.State = "Pending"
	} else if validator.Status == v1.ValidatorStateActiveOngoing {
		pageData.State = "Active"
		pageData.IsActive = true
	} else if validator.Status == v1.ValidatorStateActiveExiting {
		pageData.State = "Exiting"
		pageData.IsActive = true
	} else if validator.Status == v1.ValidatorStateActiveSlashed {
		pageData.State = "Slashed"
		pageData.IsActive = true
	} else if validator.Status == v1.ValidatorStateExitedUnslashed {
		pageData.State = "Exited"
	} else if validator.Status == v1.ValidatorStateExitedSlashed {
		pageData.State = "Slashed"
	} else {
		pageData.State = validator.Status.String()
	}

	if pageData.IsActive {
		// load activity map
		pageData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
		pageData.UpcheckMaximum = uint8(3)
	}

	// compute aggregated inclusion distance from cached blocks (last 2 epochs only)
	if pageData.IsActive || pageData.WasActive {
		inclCount, inclTotalDelay := services.GlobalBeaconService.GetValidatorInclusionDistance(validator.Index, 2)
		pageData.AttestationInclusionCount = inclCount
		if inclCount > 0 {
			pageData.AttestationInclusionAvgDelay = float64(inclTotalDelay) / float64(inclCount)
		}
	}

	if validator.Validator.ActivationEligibilityEpoch < 18446744073709551615 {
		pageData.ShowEligible = true
		pageData.EligibleEpoch = uint64(validator.Validator.ActivationEligibilityEpoch)
		pageData.EligibleTs = chainState.EpochToTime(validator.Validator.ActivationEligibilityEpoch)
	}
	if validator.Validator.ActivationEpoch < 18446744073709551615 {
		pageData.ShowActivation = true
		pageData.ActivationEpoch = uint64(validator.Validator.ActivationEpoch)
		pageData.ActivationTs = chainState.EpochToTime(validator.Validator.ActivationEpoch)
	}
	if validator.Validator.ExitEpoch < 18446744073709551615 {
		pageData.ShowExit = true
		pageData.WasActive = true
		pageData.ExitEpoch = uint64(validator.Validator.ExitEpoch)
		pageData.ExitTs = chainState.EpochToTime(validator.Validator.ExitEpoch)
	}
	if validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02 {
		pageData.ShowWithdrawAddress = true
		pageData.WithdrawAddress = validator.Validator.WithdrawalCredentials[12:]
	}

	// load latest blocks
	if pageData.TabView == "blocks" {
		pageData.RecentBlocks = make([]*models.ValidatorPageDataBlock, 0)
		blocksData := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, &dbtypes.BlockFilter{
			ProposerIndex: &validatorIndex,
			WithOrphaned:  1,
			WithMissing:   1,
		}, 0, 10, chainState.GetSpecs().SlotsPerEpoch)
		for _, blockData := range blocksData {
			var blockStatus dbtypes.SlotStatus
			if blockData.Block == nil {
				blockStatus = dbtypes.Missing
			} else {
				blockStatus = blockData.Block.Status
			}
			blockEntry := models.ValidatorPageDataBlock{
				Epoch:  uint64(chainState.EpochOfSlot(phase0.Slot(blockData.Slot))),
				Slot:   blockData.Slot,
				Ts:     chainState.SlotToTime(phase0.Slot(blockData.Slot)),
				Status: uint64(blockStatus),
			}
			if blockData.Block != nil {
				blockEntry.Graffiti = blockData.Block.Graffiti
				blockEntry.BlockRoot = fmt.Sprintf("0x%x", blockData.Block.Root)
				if blockData.Block.EthBlockNumber != nil {
					blockEntry.WithEthBlock = true
					blockEntry.EthBlock = *blockData.Block.EthBlockNumber
				}
			}
			pageData.RecentBlocks = append(pageData.RecentBlocks, &blockEntry)
		}
		pageData.RecentBlockCount = uint64(len(pageData.RecentBlocks))
	}

	// load latest attestations
	if pageData.TabView == "attestations" {
		currentEpoch := uint64(chainState.CurrentEpoch())
		cutOffEpoch := uint64(0)
		if currentEpoch > uint64(services.GlobalBeaconService.GetBeaconIndexer().GetActivityHistoryLength()) {
			cutOffEpoch = currentEpoch - uint64(services.GlobalBeaconService.GetBeaconIndexer().GetActivityHistoryLength())
		} else {
			cutOffEpoch = 0
		}

		validatorActivity, oldestActivityEpoch := services.GlobalBeaconService.GetValidatorVotingActivity(phase0.ValidatorIndex(validatorIndex))
		validatorActivityIdx := 0

		if cutOffEpoch < uint64(oldestActivityEpoch) {
			cutOffEpoch = uint64(oldestActivityEpoch)
		}

		for epochIdx := int64(currentEpoch); epochIdx >= int64(cutOffEpoch); epochIdx-- {
			epoch := phase0.Epoch(epochIdx)
			found := false

			for validatorActivityIdx < len(validatorActivity) && chainState.EpochOfSlot(validatorActivity[validatorActivityIdx].VoteBlock.Slot-phase0.Slot(validatorActivity[validatorActivityIdx].VoteDelay)) == epoch {
				found = true
				vote := validatorActivity[validatorActivityIdx]

				attestation := &models.ValidatorPageDataAttestation{
					Epoch:          uint64(epoch),
					Slot:           uint64(vote.VoteBlock.Slot - phase0.Slot(vote.VoteDelay)),
					InclusionSlot:  uint64(vote.VoteBlock.Slot),
					InclusionRoot:  vote.VoteBlock.Root[:],
					Time:           chainState.SlotToTime(vote.VoteBlock.Slot - phase0.Slot(vote.VoteDelay)),
					Status:         uint64(services.GlobalBeaconService.CheckBlockOrphanedStatus(ctx, vote.VoteBlock.Root)),
					InclusionDelay: uint64(vote.VoteDelay),
					HasDuty:        true,
				}
				pageData.RecentAttestations = append(pageData.RecentAttestations, attestation)
				validatorActivityIdx++
			}

			validatorStatus := v1.ValidatorToState(validator.Validator, &validator.Balance, epoch, beacon.FarFutureEpoch)
			if !found && strings.HasPrefix(validatorStatus.String(), "active_") {
				attestation := &models.ValidatorPageDataAttestation{
					Epoch:  uint64(epoch),
					Status: 0,
					Time:   chainState.EpochToTime(epoch),
					Missed: true,
				}

				// get epoch stats for this epoch to check for attestation duties
				epochStats := services.GlobalBeaconService.GetBeaconIndexer().GetEpochStats(epoch, nil)

				var epochStatsValues *beacon.EpochStatsValues

				if epochStats != nil {
					epochStatsValues = epochStats.GetValues(true)
				}
				if epochStatsValues != nil && epochStatsValues.AttesterDuties != nil {
					dutySlot := phase0.Slot(0)
					foundDuty := false

				dutiesLoop:
					for slotIndex, duties1 := range epochStatsValues.AttesterDuties {
						for _, duties2 := range duties1 {
							for _, validatorIndice := range duties2 {
								if epochStatsValues.ActiveIndices[validatorIndice] == phase0.ValidatorIndex(validatorIndex) {
									dutySlot = phase0.Slot(slotIndex) + chainState.EpochStartSlot(epoch)
									foundDuty = true
									break dutiesLoop
								}
							}
						}
					}

					attestation.HasDuty = foundDuty
					attestation.Slot = uint64(dutySlot)
					attestation.Time = chainState.SlotToTime(dutySlot)
				}

				if attestation.Epoch+1 >= currentEpoch {
					attestation.Scheduled = true
				}

				pageData.RecentAttestations = append(pageData.RecentAttestations, attestation)
			}
		}

		pageData.RecentAttestationCount = uint64(len(pageData.RecentAttestations))
	}

	// load recent deposits
	if pageData.TabView == "deposits" {
		// first get recent included deposits
		pageData.RecentDeposits = make([]*models.ValidatorPageDataDeposit, 0)

		depositsData, totalIncludedDeposits := services.GlobalBeaconService.GetDepositRequestsByFilter(ctx, &services.CombinedDepositRequestFilter{
			Filter: &dbtypes.DepositTxFilter{
				PublicKey:    validator.Validator.PublicKey[:],
				WithValid:    1,
				WithOrphaned: 1,
			},
		}, 0, 100)

		if totalIncludedDeposits > 10 {
			pageData.AdditionalIncludedDepositCount = totalIncludedDeposits - 10
		}

		for _, deposit := range depositsData {
			depositData := &models.ValidatorPageDataDeposit{
				PublicKey:        deposit.PublicKey(),
				WithdrawalCreds:  deposit.WithdrawalCredentials(),
				Amount:           deposit.Amount(),
				Time:             chainState.SlotToTime(phase0.Slot(deposit.Request.SlotNumber)),
				Slot:             deposit.Request.SlotNumber,
				SlotRoot:         deposit.Request.SlotRoot,
				Orphaned:         deposit.RequestOrphaned,
				DepositorAddress: deposit.SourceAddress(),
			}

			if deposit.Request != nil {
				depositData.HasIndex = true
				depositData.Index = *deposit.Request.Index
			}

			// Add queue status
			if deposit.IsQueued {
				depositData.IsQueued = true
				if !bytes.Equal(deposit.QueueEntry.PendingDeposit.Pubkey[:], deposit.PublicKey()) {
					logrus.Warnf("queue entry public key mismatch: %x != %x", deposit.QueueEntry.PendingDeposit.Pubkey[:], deposit.PublicKey())
				}

				depositData.QueuePosition = deposit.QueueEntry.QueuePos
				depositData.EstimatedTime = chainState.EpochToTime(deposit.QueueEntry.EpochEstimate)
			}

			// Add validator status
			if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey())); !found {
				depositData.ValidatorStatus = "Deposited"
				depositData.ValidatorExists = false
			} else {
				depositData.ValidatorExists = true
				depositData.ValidatorIndex = uint64(validatorIdx)
				depositData.ValidatorName = services.GlobalBeaconService.GetValidatorName(uint64(validatorIdx))

				validator := services.GlobalBeaconService.GetValidatorByIndex(validatorIdx, false)
				if strings.HasPrefix(validator.Status.String(), "pending") {
					depositData.ValidatorStatus = "Pending"
				} else if validator.Status == v1.ValidatorStateActiveOngoing {
					depositData.ValidatorStatus = "Active"
					depositData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveExiting {
					depositData.ValidatorStatus = "Exiting"
					depositData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveSlashed {
					depositData.ValidatorStatus = "Slashed"
					depositData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateExitedUnslashed {
					depositData.ValidatorStatus = "Exited"
				} else if validator.Status == v1.ValidatorStateExitedSlashed {
					depositData.ValidatorStatus = "Slashed"
				} else {
					depositData.ValidatorStatus = validator.Status.String()
				}

				if depositData.ShowUpcheck {
					depositData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					depositData.UpcheckMaximum = uint8(3)
				}
			}

			// Add transaction details if available
			if deposit.Transaction != nil {
				depositData.HasTransaction = true
				depositData.TransactionHash = deposit.Transaction.TxHash
				depositData.TransactionDetails = &models.ValidatorPageDataDepositTxDetails{
					BlockNumber: deposit.Transaction.BlockNumber,
					BlockHash:   fmt.Sprintf("%#x", deposit.Transaction.BlockRoot),
					BlockTime:   deposit.Transaction.BlockTime,
					TxOrigin:    common.Address(deposit.Transaction.TxSender).Hex(),
					TxTarget:    common.Address(deposit.Transaction.TxTarget).Hex(),
					TxHash:      fmt.Sprintf("%#x", deposit.Transaction.TxHash),
				}
				if deposit.Transaction.ValidSignature == 0 {
					depositData.InvalidSignature = true
				}
			}

			pageData.RecentDeposits = append(pageData.RecentDeposits, depositData)
		}

		pageData.RecentDepositCount = uint64(len(pageData.RecentDeposits))
	}

	// load recent withdrawal requests
	if pageData.TabView == "withdrawalrequests" {
		dbElWithdrawals, totalPendingWithdrawalTxs, totalWithdrawalReqs := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, &services.CombinedWithdrawalRequestFilter{
			Filter: &dbtypes.WithdrawalRequestFilter{
				PublicKey: validator.Validator.PublicKey[:],
			},
		}, 0, 10)
		if totalPendingWithdrawalTxs+totalWithdrawalReqs > 10 {
			pageData.AdditionalWithdrawalRequestCount = totalPendingWithdrawalTxs + totalWithdrawalReqs - 10
		}

		// helper to load tx details for withdrawal requests
		buildTxDetails := func(withdrawalTx *dbtypes.WithdrawalRequestTx) *models.ValidatorPageDataWithdrawalTxDetails {
			txDetails := &models.ValidatorPageDataWithdrawalTxDetails{
				BlockNumber: withdrawalTx.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", withdrawalTx.BlockRoot),
				BlockTime:   withdrawalTx.BlockTime,
				TxOrigin:    common.Address(withdrawalTx.TxSender).Hex(),
				TxTarget:    common.Address(withdrawalTx.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", withdrawalTx.TxHash),
			}

			return txDetails
		}

		// get head block number to calculate queue timing
		headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
		headBlockNum := uint64(0)
		if headBlock != nil && headBlock.GetBlockIndex(ctx) != nil {
			headBlockNum = uint64(headBlock.GetBlockIndex(ctx).ExecutionNumber)
		}

		for _, elWithdrawal := range dbElWithdrawals {
			elWithdrawalData := &models.ValidatorPageDataWithdrawal{
				SourceAddr: elWithdrawal.SourceAddress(),
				Amount:     elWithdrawal.Amount(),
			}

			if request := elWithdrawal.Request; request != nil {
				elWithdrawalData.IsIncluded = true
				elWithdrawalData.SlotNumber = request.SlotNumber
				elWithdrawalData.SlotRoot = request.SlotRoot
				elWithdrawalData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
				elWithdrawalData.Status = uint64(1)
				elWithdrawalData.Result = request.Result
				elWithdrawalData.ResultMessage = getWithdrawalResultMessage(request.Result, chainState.GetSpecs())
				if elWithdrawal.RequestOrphaned {
					elWithdrawalData.Status = uint64(2)
				}
			}

			if elWithdrawal.Transaction != nil {
				elWithdrawalData.TransactionHash = elWithdrawal.Transaction.TxHash
				elWithdrawalData.LinkedTransaction = true
				elWithdrawalData.TransactionDetails = buildTxDetails(elWithdrawal.Transaction)
				elWithdrawalData.TxStatus = uint64(1)
				if elWithdrawal.TransactionOrphaned {
					elWithdrawalData.TxStatus = uint64(2)
				}

				if !elWithdrawalData.IsIncluded {
					queuePos := int64(elWithdrawal.Transaction.DequeueBlock) - int64(headBlockNum)
					targetSlot := int64(chainState.CurrentSlot()) + queuePos
					if targetSlot > 0 {
						elWithdrawalData.SlotNumber = uint64(targetSlot)
						elWithdrawalData.Time = chainState.SlotToTime(phase0.Slot(targetSlot))
					}
				}
			}

			pageData.WithdrawalRequests = append(pageData.WithdrawalRequests, elWithdrawalData)
		}

		pageData.WithdrawalRequestCount = uint64(len(pageData.WithdrawalRequests))
	}

	// load recent consolidation requests
	if pageData.TabView == "consolidationrequests" {
		dbConsolidations, totalPendingConsolidationTxs, totalConsolidationReqs := services.GlobalBeaconService.GetConsolidationRequestsByFilter(ctx, &services.CombinedConsolidationRequestFilter{
			Filter: &dbtypes.ConsolidationRequestFilter{
				PublicKey: validator.Validator.PublicKey[:],
			},
		}, 0, 10)
		if totalPendingConsolidationTxs+totalConsolidationReqs > 10 {
			pageData.AdditionalConsolidationRequestCount = totalPendingConsolidationTxs + totalConsolidationReqs - 10
		}

		// helper to load tx details for consolidation requests
		buildTxDetails := func(consolidationTx *dbtypes.ConsolidationRequestTx) *models.ValidatorPageDataConsolidationTxDetails {
			txDetails := &models.ValidatorPageDataConsolidationTxDetails{
				BlockNumber: consolidationTx.BlockNumber,
				BlockHash:   fmt.Sprintf("%#x", consolidationTx.BlockRoot),
				BlockTime:   consolidationTx.BlockTime,
				TxOrigin:    common.Address(consolidationTx.TxSender).Hex(),
				TxTarget:    common.Address(consolidationTx.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("%#x", consolidationTx.TxHash),
			}

			return txDetails
		}

		// get head block number to calculate queue timing
		headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
		headBlockNum := uint64(0)
		if headBlock != nil && headBlock.GetBlockIndex(ctx) != nil {
			headBlockNum = uint64(headBlock.GetBlockIndex(ctx).ExecutionNumber)
		}

		for _, consolidation := range dbConsolidations {
			elConsolidationData := &models.ValidatorPageDataConsolidation{
				SourceAddr:      consolidation.SourceAddress(),
				SourcePublicKey: consolidation.SourcePubkey(),
				TargetPublicKey: consolidation.TargetPubkey(),
			}

			if sourceIndex := consolidation.SourceIndex(); sourceIndex != nil {
				elConsolidationData.SourceValidatorValid = true
				elConsolidationData.SourceValidatorIndex = *sourceIndex
				elConsolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*sourceIndex)
			}

			if targetIndex := consolidation.TargetIndex(); targetIndex != nil {
				elConsolidationData.TargetValidatorValid = true
				elConsolidationData.TargetValidatorIndex = *targetIndex
				elConsolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*targetIndex)
			}

			if request := consolidation.Request; request != nil {
				elConsolidationData.IsIncluded = true
				elConsolidationData.SlotNumber = request.SlotNumber
				elConsolidationData.SlotRoot = request.SlotRoot
				elConsolidationData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
				elConsolidationData.Status = uint64(1)
				elConsolidationData.Result = request.Result
				elConsolidationData.ResultMessage = getConsolidationResultMessage(request.Result, chainState.GetSpecs())
				if consolidation.RequestOrphaned {
					elConsolidationData.Status = uint64(2)
				}
			}

			if transaction := consolidation.Transaction; transaction != nil {
				elConsolidationData.TransactionHash = transaction.TxHash
				elConsolidationData.LinkedTransaction = true
				elConsolidationData.TransactionDetails = buildTxDetails(transaction)
				elConsolidationData.TxStatus = uint64(1)
				if consolidation.TransactionOrphaned {
					elConsolidationData.TxStatus = uint64(2)
				}

				if !elConsolidationData.IsIncluded {
					queuePos := int64(consolidation.Transaction.DequeueBlock) - int64(headBlockNum)
					targetSlot := int64(chainState.CurrentSlot()) + queuePos
					if targetSlot > 0 {
						elConsolidationData.SlotNumber = uint64(targetSlot)
						elConsolidationData.Time = chainState.SlotToTime(phase0.Slot(targetSlot))
					}
				}
			}

			pageData.ConsolidationRequests = append(pageData.ConsolidationRequests, elConsolidationData)
		}

		pageData.ConsolidationRequestCount = uint64(len(pageData.ConsolidationRequests))
	}

	// load recent withdrawals (beacon chain withdrawals)
	if pageData.TabView == "withdrawals" {
		withdrawalFilter := &dbtypes.WithdrawalFilter{
			MinIndex:     validatorIndex,
			MaxIndex:     validatorIndex,
			WithOrphaned: 1,
		}
		dbWithdrawals, totalRows := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, withdrawalFilter, 0, 10)
		if totalRows > 10 {
			pageData.AdditionalWithdrawalCount = totalRows - 10
		}

		// Batch resolve account IDs to addresses
		accountIDs := make([]uint64, 0, len(dbWithdrawals))
		accountIDSet := make(map[uint64]bool, len(dbWithdrawals))
		for _, w := range dbWithdrawals {
			if w.AccountID > 0 && !accountIDSet[w.AccountID] {
				accountIDSet[w.AccountID] = true
				accountIDs = append(accountIDs, w.AccountID)
			}
		}
		accountMap := make(map[uint64]*dbtypes.ElAccount, len(accountIDs))
		if len(accountIDs) > 0 {
			accounts, err := db.GetElAccountsByIDs(ctx, accountIDs)
			if err == nil {
				for _, acct := range accounts {
					accountMap[acct.ID] = acct
				}
			}
		}

		// Batch resolve blocks (including ref slot blocks)
		blockUids := make([]uint64, 0, len(dbWithdrawals)*2)
		blockUidSet := make(map[uint64]bool, len(dbWithdrawals)*2)
		for _, w := range dbWithdrawals {
			if !blockUidSet[w.BlockUid] {
				blockUidSet[w.BlockUid] = true
				blockUids = append(blockUids, w.BlockUid)
			}
			if w.RefSlot != nil && !blockUidSet[*w.RefSlot] {
				blockUidSet[*w.RefSlot] = true
				blockUids = append(blockUids, *w.RefSlot)
			}
		}
		blockMap := make(map[uint64]*dbtypes.AssignedSlot, len(blockUids))
		if len(blockUids) > 0 {
			blockFilter := &dbtypes.BlockFilter{
				BlockUids:    blockUids,
				WithOrphaned: 1,
			}
			blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, blockFilter, 0, uint32(len(blockUids)), 0)
			for _, b := range blocks {
				if b.Block != nil {
					blockMap[b.Block.BlockUid] = b
				}
			}
		}

		for _, withdrawal := range dbWithdrawals {
			slot := withdrawal.BlockUid >> 16
			withdrawalData := &models.ValidatorPageDataBeaconWithdrawal{
				SlotNumber: slot,
				Time:       chainState.SlotToTime(phase0.Slot(slot)),
				Orphaned:   withdrawal.Orphaned,
				Type:       withdrawal.Type,
				Amount:     withdrawal.Amount,
			}

			hasAddress := false
			if withdrawal.AccountID > 0 {
				if acct, ok := accountMap[withdrawal.AccountID]; ok {
					withdrawalData.Address = acct.Address
					hasAddress = true
				}
			}
			if !hasAddress && withdrawal.Address != nil {
				withdrawalData.Address = withdrawal.Address
			}

			if blockInfo, ok := blockMap[withdrawal.BlockUid]; ok && blockInfo.Block != nil {
				withdrawalData.BlockRoot = blockInfo.Block.Root
				if blockInfo.Block.EthBlockNumber != nil {
					withdrawalData.BlockNumber = *blockInfo.Block.EthBlockNumber
				}
			}

			if withdrawal.RefSlot != nil {
				withdrawalData.RefSlot = *withdrawal.RefSlot >> 16
				if refBlock, ok := blockMap[*withdrawal.RefSlot]; ok && refBlock.Block != nil {
					withdrawalData.RefSlotRoot = refBlock.Block.Root
				}
			}

			pageData.Withdrawals = append(pageData.Withdrawals, withdrawalData)
		}

		pageData.WithdrawalCount = uint64(len(pageData.Withdrawals))
	}

	// Check for exit reason if validator is exiting or has exited
	if pageData.ShowExit {
		zeroAmount := uint64(0)
		exitSlot := uint64(chainState.EpochToSlot(validator.Validator.ExitEpoch))

		// Check for slashing
		if slashings, totalSlashings := services.GlobalBeaconService.GetSlashingsByFilter(ctx, &dbtypes.SlashingFilter{
			MinIndex: validatorIndex,
			MaxIndex: validatorIndex,
		}, 0, 1); totalSlashings > 0 && len(slashings) > 0 {
			pageData.ExitReason = "Validator was slashed"
			pageData.ExitReasonSlashing = true
			pageData.ExitReasonSlot = slashings[0].SlotNumber
			pageData.ExitReasonSlashingReason = uint64(slashings[0].Reason)

			// Check for voluntary exit
		} else if exits, totalExits := services.GlobalBeaconService.GetVoluntaryExitsByFilter(ctx, &dbtypes.VoluntaryExitFilter{
			MinIndex: validatorIndex,
			MaxIndex: validatorIndex,
		}, 0, 1); totalExits > 0 && len(exits) > 0 {
			pageData.ExitReason = "Validator submitted a voluntary exit request"
			pageData.ExitReasonVoluntaryExit = true
			pageData.ExitReasonSlot = exits[0].SlotNumber

			// Check for full withdrawal request
		} else if withdrawals, totalPendingWithdrawalTxs, totalWithdrawalReqs := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, &services.CombinedWithdrawalRequestFilter{
			Filter: &dbtypes.WithdrawalRequestFilter{
				PublicKey:     validator.Validator.PublicKey[:],
				SourceAddress: pageData.WithdrawAddress,
				MaxAmount:     &zeroAmount,
				MaxSlot:       exitSlot,
			},
		}, 0, 1); totalPendingWithdrawalTxs+totalWithdrawalReqs > 0 && len(withdrawals) > 0 && pageData.ShowWithdrawAddress {
			withdrawal := withdrawals[0]
			pageData.ExitReason = "Validator submitted a full withdrawal request"
			pageData.ExitReasonWithdrawal = true
			pageData.ExitReasonSlot = withdrawal.Request.SlotNumber

			if withdrawal.Transaction != nil {
				pageData.ExitReasonTxHash = withdrawal.Transaction.TxHash
				pageData.ExitReasonTxDetails = &models.ValidatorPageDataWithdrawalTxDetails{
					BlockNumber: withdrawal.Transaction.BlockNumber,
					BlockHash:   fmt.Sprintf("%#x", withdrawal.Transaction.BlockRoot),
					BlockTime:   withdrawal.Transaction.BlockTime,
					TxOrigin:    common.Address(withdrawal.Transaction.TxSender).Hex(),
					TxTarget:    common.Address(withdrawal.Transaction.TxTarget).Hex(),
					TxHash:      fmt.Sprintf("%#x", withdrawal.Transaction.TxHash),
				}
			}
			// Check for consolidation request
		} else if consolidations, totalPendingConsolidationTxs, totalConsolidationReqs := services.GlobalBeaconService.GetConsolidationRequestsByFilter(ctx, &services.CombinedConsolidationRequestFilter{
			Filter: &dbtypes.ConsolidationRequestFilter{
				PublicKey:     validator.Validator.PublicKey[:],
				SourceAddress: pageData.WithdrawAddress,
				MaxSlot:       exitSlot,
			},
		}, 0, 1); totalPendingConsolidationTxs+totalConsolidationReqs > 0 && len(consolidations) > 0 && pageData.ShowWithdrawAddress {
			consolidation := consolidations[0]
			pageData.ExitReason = "Validator was consolidated"
			pageData.ExitReasonConsolidation = true
			pageData.ExitReasonSlot = consolidation.Request.SlotNumber

			if targetIndex := consolidation.TargetIndex(); targetIndex != nil {
				pageData.ExitReasonTargetIndex = *targetIndex
				pageData.ExitReasonTargetName = services.GlobalBeaconService.GetValidatorName(*targetIndex)
			}

			if consolidation.Transaction != nil {
				pageData.ExitReasonTxHash = consolidation.Transaction.TxHash
				pageData.ExitReasonTxDetails = &models.ValidatorPageDataWithdrawalTxDetails{
					BlockNumber: consolidation.Transaction.BlockNumber,
					BlockHash:   fmt.Sprintf("%#x", consolidation.Transaction.BlockRoot),
					BlockTime:   consolidation.Transaction.BlockTime,
					TxOrigin:    common.Address(consolidation.Transaction.TxSender).Hex(),
					TxTarget:    common.Address(consolidation.Transaction.TxTarget).Hex(),
					TxHash:      fmt.Sprintf("%#x", consolidation.Transaction.TxHash),
				}
			}
		}
	}

	return pageData, 10 * time.Minute
}
