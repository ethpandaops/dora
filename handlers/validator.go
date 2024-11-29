package handlers

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"
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
		data.Data, pageError = getValidatorPageData(uint64(validator.Index), tabView)
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
		pageData, cacheTimeout := buildValidatorPageData(validatorIndex, tabView)
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

func buildValidatorPageData(validatorIndex uint64, tabView string) (*models.ValidatorPageData, time.Duration) {
	logrus.Debugf("validator page called: %v", validatorIndex)

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), true)

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
	if validator.Validator.WithdrawalCredentials[0] == 0x01 {
		pageData.ShowWithdrawAddress = true
		pageData.WithdrawAddress = validator.Validator.WithdrawalCredentials[12:]
	}

	// load latest blocks
	if pageData.TabView == "blocks" {
		pageData.RecentBlocks = make([]*models.ValidatorPageDataBlock, 0)
		blocksData := services.GlobalBeaconService.GetDbBlocksByFilter(&dbtypes.BlockFilter{
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
		if currentEpoch > uint64(services.GlobalBeaconService.GetBeaconIndexer().GetInMemoryEpochs()) {
			cutOffEpoch = currentEpoch - uint64(services.GlobalBeaconService.GetBeaconIndexer().GetInMemoryEpochs())
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
					Status:         uint64(services.GlobalBeaconService.CheckBlockOrphanedStatus(vote.VoteBlock.Root)),
					InclusionDelay: uint64(vote.VoteDelay),
					HasDuty:        true,
				}
				pageData.RecentAttestations = append(pageData.RecentAttestations, attestation)
				validatorActivityIdx++
			}
			if !found {
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

		// helper to load tx details for included deposits
		buildTxDetails := func(depositTx *dbtypes.DepositTx) *models.ValidatorPageDataDepositTxDetails {
			txDetails := &models.ValidatorPageDataDepositTxDetails{
				BlockNumber: depositTx.BlockNumber,
				BlockHash:   fmt.Sprintf("0x%x", depositTx.BlockRoot),
				BlockTime:   depositTx.BlockTime,
				TxOrigin:    common.Address(depositTx.TxSender).Hex(),
				TxTarget:    common.Address(depositTx.TxTarget).Hex(),
				TxHash:      fmt.Sprintf("0x%x", depositTx.TxHash),
			}

			return txDetails
		}

		depositSyncState := dbtypes.DepositIndexerState{}
		db.GetExplorerState("indexer.depositstate", &depositSyncState)

		depositsData, totalIncludedDeposits := services.GlobalBeaconService.GetIncludedDepositsByFilter(&dbtypes.DepositFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}, 0, 100)

		if totalIncludedDeposits > 10 {
			pageData.AdditionalIncludedDepositCount = totalIncludedDeposits - 10
		}

		initiatedFilter := &dbtypes.DepositTxFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}

		if len(depositsData) > 0 && depositsData[0].Index != nil {
			initiatedFilter.MinIndex = *depositsData[0].Index + 1
		}

		initiatedDeposits, totalInitiatedDeposits, _ := db.GetDepositTxsFiltered(0, 10, depositSyncState.FinalBlock, initiatedFilter)
		if totalInitiatedDeposits > 10 {
			pageData.AdditionalInitiatedDepositCount = totalInitiatedDeposits - 10
		}

		for _, deposit := range initiatedDeposits {
			txStatus := uint64(1)
			if deposit.Orphaned {
				txStatus = uint64(2)
			}

			pageData.RecentDeposits = append(pageData.RecentDeposits, &models.ValidatorPageDataDeposit{
				Index:           uint64(deposit.Index),
				HasIndex:        true,
				Time:            time.Unix(int64(deposit.BlockTime), 0),
				Amount:          deposit.Amount,
				WithdrawalCreds: deposit.WithdrawalCredentials,
				TxStatus:        txStatus,
				TxDetails:       buildTxDetails(deposit),
				TxHash:          deposit.TxHash,
			})
		}

		minDepositIndex := uint64(math.MaxUint64)
		maxDepositIndex := uint64(0)
		recentDepositsMap := make(map[uint64]*models.ValidatorPageDataDeposit)

		for _, deposit := range depositsData {
			blockStatus := uint64(1)
			if deposit.Orphaned {
				blockStatus = uint64(2)
			}

			depositData := &models.ValidatorPageDataDeposit{
				IsIncluded:      true,
				Slot:            uint64(deposit.SlotNumber),
				Time:            chainState.SlotToTime(phase0.Slot(deposit.SlotNumber)),
				Amount:          deposit.Amount,
				WithdrawalCreds: deposit.WithdrawalCredentials,
				Status:          blockStatus,
			}

			if deposit.Index != nil {
				depositData.HasIndex = true
				depositData.Index = *deposit.Index

				if *deposit.Index < minDepositIndex {
					minDepositIndex = *deposit.Index
				}
				if *deposit.Index > maxDepositIndex {
					maxDepositIndex = *deposit.Index
				}

				recentDepositsMap[*deposit.Index] = depositData
			}

			pageData.RecentDeposits = append(pageData.RecentDeposits, depositData)
		}

		if minDepositIndex < math.MaxUint64 {
			depositTxs, _, _ := db.GetDepositTxsFiltered(0, 10, depositSyncState.FinalBlock, &dbtypes.DepositTxFilter{
				MinIndex:  minDepositIndex,
				MaxIndex:  maxDepositIndex,
				PublicKey: validator.Validator.PublicKey[:],
			})

			for _, depositTx := range depositTxs {
				recentDeposit := recentDepositsMap[depositTx.Index]
				if recentDeposit == nil {
					continue
				}

				txStatus := uint64(1)
				if depositTx.Orphaned {
					txStatus = uint64(2)
				}

				recentDeposit.TxStatus = txStatus
				recentDeposit.TxDetails = buildTxDetails(depositTx)
				recentDeposit.TxHash = depositTx.TxHash
			}
		}

		pageData.RecentDepositCount = uint64(len(pageData.RecentDeposits))
	}

	// load recent withdrawal requests
	if pageData.TabView == "withdrawalrequests" {
		dbElWithdrawals, totalElWithdrawals := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(&dbtypes.WithdrawalRequestFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}, 0, 10)
		if totalElWithdrawals > 10 {
			pageData.AdditionalWithdrawalRequestCount = totalElWithdrawals - 10
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

		initiatedFilter := &dbtypes.WithdrawalRequestTxFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}

		if len(dbElWithdrawals) > 0 {
			initiatedFilter.MinDequeue = dbElWithdrawals[0].BlockNumber + 1
		}

		canonicalForkKeys := services.GlobalBeaconService.GetCanonicalForkIds()
		canonicalForkIds := make([]uint64, len(canonicalForkKeys))
		for idx, forkKey := range canonicalForkKeys {
			canonicalForkIds[idx] = uint64(forkKey)
		}
		isCanonical := func(forkId uint64) bool {
			for _, canonicalForkId := range canonicalForkIds {
				if canonicalForkId == forkId {
					return true
				}
			}
			return false
		}

		headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
		headBlockNum := uint64(0)
		if headBlock != nil && headBlock.GetBlockIndex() != nil {
			headBlockNum = uint64(headBlock.GetBlockIndex().ExecutionNumber)
		}

		initiatedWithdrawals, totalInitiatedWithdrawals, _ := db.GetWithdrawalRequestTxsFiltered(0, 20, canonicalForkIds, initiatedFilter)
		if totalInitiatedWithdrawals > 20 {
			pageData.AdditionalWithdrawalRequestTxCount = totalInitiatedWithdrawals - 20
		}

		for _, withdrawal := range initiatedWithdrawals {
			txStatus := uint64(1)
			if !isCanonical(withdrawal.ForkId) {
				txStatus = uint64(2)
			}

			elWithdrawalData := &models.ValidatorPageDataWithdrawal{
				SlotNumber:         0,
				Time:               time.Unix(int64(withdrawal.BlockTime), 0),
				SourceAddr:         withdrawal.SourceAddress,
				PublicKey:          withdrawal.ValidatorPubkey,
				Amount:             withdrawal.Amount,
				TxStatus:           txStatus,
				TransactionDetails: buildTxDetails(withdrawal),
				TransactionHash:    withdrawal.TxHash,
				LinkedTransaction:  true,
			}

			if headBlockNum > 0 && withdrawal.DequeueBlock > headBlockNum {
				queuePos := withdrawal.DequeueBlock - headBlockNum
				elWithdrawalData.SlotNumber = uint64(chainState.CurrentSlot()) + uint64(queuePos)
			}

			if withdrawal.ValidatorIndex != nil {
				elWithdrawalData.ValidatorValid = true
				elWithdrawalData.ValidatorIndex = *withdrawal.ValidatorIndex
				elWithdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*withdrawal.ValidatorIndex)
			}

			pageData.WithdrawalRequests = append(pageData.WithdrawalRequests, elWithdrawalData)
		}

		chainState := services.GlobalBeaconService.GetChainState()
		matcherHeight := services.GlobalBeaconService.GetWithdrawalIndexer().GetMatcherHeight()

		requestTxDetailsFor := [][]byte{}

		for _, elWithdrawal := range dbElWithdrawals {
			status := uint64(1)
			if !isCanonical(elWithdrawal.ForkId) {
				status = uint64(2)
			}

			elWithdrawalData := &models.ValidatorPageDataWithdrawal{
				IsIncluded:      true,
				SlotNumber:      elWithdrawal.SlotNumber,
				SlotRoot:        elWithdrawal.SlotRoot,
				Status:          status,
				Time:            chainState.SlotToTime(phase0.Slot(elWithdrawal.SlotNumber)),
				SourceAddr:      elWithdrawal.SourceAddress,
				Amount:          elWithdrawal.Amount,
				PublicKey:       elWithdrawal.ValidatorPubkey,
				TransactionHash: elWithdrawal.TxHash,
			}

			if elWithdrawal.ValidatorIndex != nil {
				elWithdrawalData.ValidatorIndex = *elWithdrawal.ValidatorIndex
				elWithdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*elWithdrawal.ValidatorIndex)
				elWithdrawalData.ValidatorValid = true
			}

			if len(elWithdrawalData.TransactionHash) > 0 {
				elWithdrawalData.LinkedTransaction = true
				requestTxDetailsFor = append(requestTxDetailsFor, elWithdrawalData.TransactionHash)
			} else if elWithdrawal.BlockNumber > matcherHeight {
				// withdrawal request has not been matched with a tx yet, try to find the tx on the fly
				withdrawalRequestTx := db.GetWithdrawalRequestTxsByDequeueRange(elWithdrawal.BlockNumber, elWithdrawal.BlockNumber)
				if len(withdrawalRequestTx) > 1 {
					forkIds := services.GlobalBeaconService.GetParentForkIds(beacon.ForkKey(elWithdrawal.ForkId))
					isParentFork := func(forkId uint64) bool {
						for _, parentForkId := range forkIds {
							if uint64(parentForkId) == forkId {
								return true
							}
						}
						return false
					}

					matchingTxs := []*dbtypes.WithdrawalRequestTx{}
					for _, tx := range withdrawalRequestTx {
						if isParentFork(tx.ForkId) {
							matchingTxs = append(matchingTxs, tx)
						}
					}

					if len(matchingTxs) >= int(elWithdrawal.SlotIndex)+1 {
						elWithdrawalData.TransactionHash = matchingTxs[elWithdrawal.SlotIndex].TxHash
						elWithdrawalData.LinkedTransaction = true
						elWithdrawalData.TransactionDetails = buildTxDetails(matchingTxs[elWithdrawal.SlotIndex])
					}
				} else if len(withdrawalRequestTx) == 1 {
					elWithdrawalData.TransactionHash = withdrawalRequestTx[0].TxHash
					elWithdrawalData.LinkedTransaction = true
					elWithdrawalData.TransactionDetails = buildTxDetails(withdrawalRequestTx[0])
				}
			}

			pageData.WithdrawalRequests = append(pageData.WithdrawalRequests, elWithdrawalData)
		}
		pageData.WithdrawalRequestCount = uint64(len(pageData.WithdrawalRequests))

		// load tx details for withdrawal requests
		if len(requestTxDetailsFor) > 0 {
			for _, txDetails := range db.GetWithdrawalRequestTxsByTxHashes(requestTxDetailsFor) {
				for _, elWithdrawal := range pageData.WithdrawalRequests {
					if elWithdrawal.TransactionHash != nil && bytes.Equal(elWithdrawal.TransactionHash, txDetails.TxHash) {
						elWithdrawal.TransactionDetails = buildTxDetails(txDetails)
					}
				}
			}
		}
	}

	// load recent consolidation requests
	if pageData.TabView == "consolidationrequests" {
		dbElConsolidations, totalElConsolidations := services.GlobalBeaconService.GetConsolidationRequestsByFilter(&dbtypes.ConsolidationRequestFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}, 0, 10)
		if totalElConsolidations > 10 {
			pageData.AdditionalConsolidationRequestTxCount = totalElConsolidations - 10
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

		initiatedFilter := &dbtypes.ConsolidationRequestTxFilter{
			PublicKey: validator.Validator.PublicKey[:],
		}

		if len(dbElConsolidations) > 0 {
			initiatedFilter.MinDequeue = dbElConsolidations[0].BlockNumber + 1
		}

		canonicalForkKeys := services.GlobalBeaconService.GetCanonicalForkIds()
		canonicalForkIds := make([]uint64, len(canonicalForkKeys))
		for idx, forkKey := range canonicalForkKeys {
			canonicalForkIds[idx] = uint64(forkKey)
		}
		isCanonical := func(forkId uint64) bool {
			for _, canonicalForkId := range canonicalForkIds {
				if canonicalForkId == forkId {
					return true
				}
			}
			return false
		}

		headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
		headBlockNum := uint64(0)
		if headBlock != nil && headBlock.GetBlockIndex() != nil {
			headBlockNum = uint64(headBlock.GetBlockIndex().ExecutionNumber)
		}

		initiatedConsolidations, totalInitiatedConsolidations, _ := db.GetConsolidationRequestTxsFiltered(0, 20, canonicalForkIds, initiatedFilter)
		if totalInitiatedConsolidations > 20 {
			pageData.AdditionalConsolidationRequestTxCount = totalInitiatedConsolidations - 20
		}

		for _, consolidation := range initiatedConsolidations {
			txStatus := uint64(1)
			if !isCanonical(consolidation.ForkId) {
				txStatus = uint64(2)
			}

			elConsolidationData := &models.ValidatorPageDataConsolidation{
				SlotNumber:         0,
				Time:               time.Unix(int64(consolidation.BlockTime), 0),
				SourceAddr:         consolidation.SourceAddress,
				SourcePublicKey:    consolidation.SourcePubkey,
				TargetPublicKey:    consolidation.TargetPubkey,
				TxStatus:           txStatus,
				TransactionDetails: buildTxDetails(consolidation),
				TransactionHash:    consolidation.TxHash,
				LinkedTransaction:  true,
			}

			if headBlockNum > 0 && consolidation.DequeueBlock > headBlockNum {
				queuePos := consolidation.DequeueBlock - headBlockNum
				elConsolidationData.SlotNumber = uint64(chainState.CurrentSlot()) + uint64(queuePos)
			}

			if consolidation.SourceIndex != nil {
				elConsolidationData.SourceValidatorValid = true
				elConsolidationData.SourceValidatorIndex = *consolidation.SourceIndex
				elConsolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*consolidation.SourceIndex)
			}

			if consolidation.TargetIndex != nil {
				elConsolidationData.TargetValidatorValid = true
				elConsolidationData.TargetValidatorIndex = *consolidation.TargetIndex
				elConsolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*consolidation.TargetIndex)
			}

			pageData.ConsolidationRequests = append(pageData.ConsolidationRequests, elConsolidationData)
		}

		chainState := services.GlobalBeaconService.GetChainState()
		matcherHeight := services.GlobalBeaconService.GetConsolidationIndexer().GetMatcherHeight()

		requestTxDetailsFor := [][]byte{}

		for _, elConsolidation := range dbElConsolidations {
			status := uint64(1)
			if !isCanonical(elConsolidation.ForkId) {
				status = uint64(2)
			}

			elConsolidationData := &models.ValidatorPageDataConsolidation{
				IsIncluded:      true,
				SlotNumber:      elConsolidation.SlotNumber,
				SlotRoot:        elConsolidation.SlotRoot,
				Time:            chainState.SlotToTime(phase0.Slot(elConsolidation.SlotNumber)),
				Status:          status,
				SourceAddr:      elConsolidation.SourceAddress,
				SourcePublicKey: elConsolidation.SourcePubkey,
				TargetPublicKey: elConsolidation.TargetPubkey,
				TransactionHash: elConsolidation.TxHash,
			}

			if elConsolidation.SourceIndex != nil {
				elConsolidationData.SourceValidatorIndex = *elConsolidation.SourceIndex
				elConsolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*elConsolidation.SourceIndex)
				elConsolidationData.SourceValidatorValid = true
			}

			if elConsolidation.TargetIndex != nil {
				elConsolidationData.TargetValidatorIndex = *elConsolidation.TargetIndex
				elConsolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*elConsolidation.TargetIndex)
				elConsolidationData.TargetValidatorValid = true
			}

			if len(elConsolidationData.TransactionHash) > 0 {
				elConsolidationData.LinkedTransaction = true
				requestTxDetailsFor = append(requestTxDetailsFor, elConsolidationData.TransactionHash)
			} else if elConsolidation.BlockNumber > matcherHeight {
				// consolidation request has not been matched with a tx yet, try to find the tx on the fly
				consolidationRequestTx := db.GetConsolidationRequestTxsByDequeueRange(elConsolidation.BlockNumber, elConsolidation.BlockNumber)
				if len(consolidationRequestTx) > 1 {
					forkIds := services.GlobalBeaconService.GetParentForkIds(beacon.ForkKey(elConsolidation.ForkId))
					isParentFork := func(forkId uint64) bool {
						for _, parentForkId := range forkIds {
							if uint64(parentForkId) == forkId {
								return true
							}
						}
						return false
					}

					matchingTxs := []*dbtypes.ConsolidationRequestTx{}
					for _, tx := range consolidationRequestTx {
						if isParentFork(tx.ForkId) {
							matchingTxs = append(matchingTxs, tx)
						}
					}

					if len(matchingTxs) >= int(elConsolidation.SlotIndex)+1 {
						elConsolidationData.TransactionHash = matchingTxs[elConsolidation.SlotIndex].TxHash
						elConsolidationData.LinkedTransaction = true
						elConsolidationData.TransactionDetails = buildTxDetails(matchingTxs[elConsolidation.SlotIndex])
					}

				} else if len(consolidationRequestTx) == 1 {
					elConsolidationData.TransactionHash = consolidationRequestTx[0].TxHash
					elConsolidationData.LinkedTransaction = true
					elConsolidationData.TransactionDetails = buildTxDetails(consolidationRequestTx[0])
				}
			}

			pageData.ConsolidationRequests = append(pageData.ConsolidationRequests, elConsolidationData)
		}
		pageData.ConsolidationRequestCount = uint64(len(pageData.ConsolidationRequests))

		// load tx details for consolidation requests
		if len(requestTxDetailsFor) > 0 {
			for _, txDetails := range db.GetConsolidationRequestTxsByTxHashes(requestTxDetailsFor) {
				for _, elConsolidation := range pageData.ConsolidationRequests {
					if elConsolidation.TransactionHash != nil && bytes.Equal(elConsolidation.TransactionHash, txDetails.TxHash) {
						elConsolidation.TransactionDetails = buildTxDetails(txDetails)
					}
				}
			}
		}
	}

	return pageData, 10 * time.Minute
}
