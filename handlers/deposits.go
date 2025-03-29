package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Deposits will return the main "deposits" page using a go template
func Deposits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"deposits/deposits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/deposits", "Deposits", templateFiles)

	urlArgs := r.URL.Query()
	var firstEpoch uint64 = math.MaxUint64
	if urlArgs.Has("epoch") {
		firstEpoch, _ = strconv.ParseUint(urlArgs.Get("epoch"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("count") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("count"), 10, 64)
	}

	// Get tab view from URL
	tabView := "included"
	if urlArgs.Has("v") {
		tabView = urlArgs.Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getDepositsPageData(firstEpoch, pageSize, tabView)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "deposits.go", "Deposits", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "deposits.go", "Deposits", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getDepositsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.DepositsPageData, error) {
	pageData := &models.DepositsPageData{}
	pageCacheKey := fmt.Sprintf("deposits:%v:%v:%v", firstEpoch, pageSize, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildDepositsPageData(firstEpoch, pageSize, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.DepositsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildDepositsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.DepositsPageData, time.Duration) {
	logrus.Debugf("deposits page called: %v:%v:%v", firstEpoch, pageSize, tabView)
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()
	electraActive := specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch <= uint64(currentEpoch)

	pageData := &models.DepositsPageData{
		IsElectraActive: electraActive,
		TabView:         tabView,
	}

	var recentEpochStatsValues *beacon.EpochStatsValues
	epochStatsEpoch := currentEpoch
	for epochStatsEpoch+3 > currentEpoch {
		recentEpochStats := services.GlobalBeaconService.GetBeaconIndexer().GetEpochStats(epochStatsEpoch, nil)
		if recentEpochStats != nil {
			recentEpochStatsValues = recentEpochStats.GetValues(false)
			if recentEpochStatsValues != nil {
				break
			}
		}
		if epochStatsEpoch == 0 {
			break
		}
		epochStatsEpoch--
	}

	var activeValidatorCount uint64
	var totalEligibleEther uint64
	if recentEpochStatsValues != nil {
		activeValidatorCount = recentEpochStatsValues.ActiveValidators
		totalEligibleEther = uint64(recentEpochStatsValues.EffectiveBalance)
	}

	activationQueueLength, _ := services.GlobalBeaconService.GetBeaconIndexer().GetActivationExitQueueLengths(currentEpoch, nil)
	pageData.EnteringValidatorCount = activationQueueLength

	if specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch <= uint64(currentEpoch) {
		// electra deposit queue
		depositQueue := services.GlobalBeaconService.GetBeaconIndexer().GetLatestDepositQueue(nil)
		if depositQueue != nil {
			depositAmount := phase0.Gwei(0)
			validatorCount := uint64(0)

			newValidators := map[phase0.BLSPubKey]interface{}{}
			for _, deposit := range depositQueue {
				depositAmount += deposit.Amount
				_, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(deposit.Pubkey)
				if !found {
					_, isNew := newValidators[deposit.Pubkey]
					if !isNew {
						newValidators[deposit.Pubkey] = nil
						validatorCount++
					}
				}
			}

			pageData.EnteringValidatorCount += validatorCount
			pageData.EnteringEtherAmount = uint64(depositAmount)
			pageData.EtherChurnPerEpoch = chainState.GetActivationExitChurnLimit(totalEligibleEther)
			pageData.EtherChurnPerDay = pageData.EtherChurnPerEpoch * 225

			estQueueEpochDuration := phase0.Epoch(uint64(depositAmount) / pageData.EtherChurnPerEpoch)
			pageData.NewDepositProcessAfter = chainState.EpochToTime(currentEpoch + estQueueEpochDuration)
		}
	} else {
		// pre-electra
		pageData.ValidatorsPerEpoch = chainState.GetValidatorChurnLimit(activeValidatorCount)
		pageData.ValidatorsPerDay = pageData.ValidatorsPerEpoch * 225

		estQueueEpochDuration := phase0.Epoch(uint64(pageData.EnteringValidatorCount) / pageData.ValidatorsPerEpoch)
		pageData.NewDepositProcessAfter = chainState.EpochToTime(currentEpoch + estQueueEpochDuration)
	}

	// Only load data for the selected tab
	switch tabView {
	case "transactions":
		// load initiated deposits
		dbDepositTxs := db.GetDepositTxs(0, 20)
		for _, depositTx := range dbDepositTxs {
			depositTxData := &models.DepositsPageDataInitiatedDeposit{
				Index:                 depositTx.Index,
				Address:               depositTx.TxSender,
				PublicKey:             depositTx.PublicKey,
				Withdrawalcredentials: depositTx.WithdrawalCredentials,
				Amount:                depositTx.Amount,
				TxHash:                depositTx.TxHash,
				Time:                  time.Unix(int64(depositTx.BlockTime), 0),
				Block:                 depositTx.BlockNumber,
				Orphaned:              depositTx.Orphaned,
				Valid:                 depositTx.ValidSignature == 1 || depositTx.ValidSignature == 2,
			}

			validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(depositTx.PublicKey))
			if !found {
				depositTxData.ValidatorStatus = "Deposited"
			} else {
				validator := services.GlobalBeaconService.GetValidatorByIndex(validatorIndex, false)
				if strings.HasPrefix(validator.Status.String(), "pending") {
					depositTxData.ValidatorStatus = "Pending"
				} else if validator.Status == v1.ValidatorStateActiveOngoing {
					depositTxData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveExiting {
					depositTxData.ValidatorStatus = "Exiting"
					depositTxData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveSlashed {
					depositTxData.ValidatorStatus = "Slashed"
					depositTxData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateExitedUnslashed {
					depositTxData.ValidatorStatus = "Exited"
				} else if validator.Status == v1.ValidatorStateExitedSlashed {
					depositTxData.ValidatorStatus = "Slashed"
				} else {
					depositTxData.ValidatorStatus = validator.Status.String()
				}

				if depositTxData.ShowUpcheck {
					depositTxData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					depositTxData.UpcheckMaximum = uint8(3)
				}
			}

			pageData.InitiatedDeposits = append(pageData.InitiatedDeposits, depositTxData)
		}
		pageData.InitiatedDepositCount = uint64(len(pageData.InitiatedDeposits))

	case "included":
		// load included deposits
		depositFilter := &services.CombinedDepositRequestFilter{
			Filter: &dbtypes.DepositTxFilter{
				WithOrphaned: 0,
			},
		}

		dbDeposits, _ := services.GlobalBeaconService.GetDepositRequestsByFilter(depositFilter, 0, uint32(20))
		for _, deposit := range dbDeposits {
			depositData := &models.DepositsPageDataIncludedDeposit{
				PublicKey:             deposit.PublicKey(),
				Withdrawalcredentials: deposit.WithdrawalCredentials(),
				Amount:                deposit.Amount(),
				Time:                  chainState.SlotToTime(phase0.Slot(deposit.Request.SlotNumber)),
				SlotNumber:            deposit.Request.SlotNumber,
				SlotRoot:              deposit.Request.SlotRoot,
				Orphaned:              deposit.RequestOrphaned,
				DepositorAddress:      deposit.SourceAddress(),
			}

			if deposit.IsQueued {
				depositData.IsQueued = true
				depositData.QueuePosition = deposit.QueueEntry.QueuePos
				depositData.EstimatedTime = deposit.QueueEntry.TimeEstimate
			}

			if deposit.Request.Index != nil {
				depositData.HasIndex = true
				depositData.Index = *deposit.Request.Index
			}

			if deposit.Transaction != nil {
				depositData.HasTransaction = true
				depositData.TransactionDetails = &models.DepositsPageDataIncludedDepositTxDetails{
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

			validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey()))
			if !found {
				depositData.ValidatorStatus = "Deposited"
			} else {
				validator := services.GlobalBeaconService.GetValidatorByIndex(validatorIndex, false)
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

			pageData.IncludedDeposits = append(pageData.IncludedDeposits, depositData)
		}
		pageData.IncludedDepositCount = uint64(len(pageData.IncludedDeposits))

	case "queue":
		if pageData.IsElectraActive {
			headBlock := services.GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
			queuedDeposits := services.GlobalBeaconService.GetIndexedDepositQueue(headBlock)
			depositIndexes := make([]uint64, 0)
			limit := len(queuedDeposits)
			if limit > 20 {
				limit = 20
			}

			for i := 0; i < limit; i++ {
				queueEntry := queuedDeposits[i]
				if queueEntry.DepositIndex == nil {
					continue
				}
				depositIndexes = append(depositIndexes, *queueEntry.DepositIndex)
			}

			txDetailsMap := map[uint64]*dbtypes.DepositTx{}
			for _, txDetail := range db.GetDepositTxsByIndexes(depositIndexes) {
				txDetailsMap[txDetail.Index] = txDetail
			}

			for _, queueEntry := range queuedDeposits[:limit] {
				depositData := &models.DepositsPageDataQueuedDeposit{
					QueuePosition:         queueEntry.QueuePos,
					EstimatedTime:         queueEntry.TimeEstimate,
					PublicKey:             queueEntry.PendingDeposit.Pubkey[:],
					Withdrawalcredentials: queueEntry.PendingDeposit.WithdrawalCredentials[:],
					Amount:                uint64(queueEntry.PendingDeposit.Amount),
				}

				if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(depositData.PublicKey)); !found {
					depositData.ValidatorStatus = "Deposited"
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

				if queueEntry.DepositIndex != nil {
					depositData.HasIndex = true
					depositData.Index = *queueEntry.DepositIndex

					if tx, txFound := txDetailsMap[depositData.Index]; txFound {
						depositData.HasTransaction = true
						depositData.TransactionHash = tx.TxHash
						depositData.Withdrawalcredentials = tx.WithdrawalCredentials
						depositData.TransactionDetails = &models.DepositsPageDataQueuedDepositTxDetails{
							BlockNumber: tx.BlockNumber,
							BlockHash:   fmt.Sprintf("%#x", tx.BlockRoot),
							BlockTime:   tx.BlockTime,
							TxOrigin:    common.Address(tx.TxSender).Hex(),
							TxTarget:    common.Address(tx.TxTarget).Hex(),
							TxHash:      fmt.Sprintf("%#x", tx.TxHash),
						}
					}
				}

				pageData.QueuedDeposits = append(pageData.QueuedDeposits, depositData)
			}
			pageData.QueuedDepositCount = uint64(len(pageData.QueuedDeposits))
		}
	}

	return pageData, 1 * time.Minute
}
