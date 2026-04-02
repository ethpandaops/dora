package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Withdrawals will return the main "withdrawals" page using a go template
func Withdrawals(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"withdrawals/withdrawals.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/withdrawals", "Withdrawals", templateFiles)

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
	tabView := "beaconwithdrawals"
	if urlArgs.Has("v") {
		tabView = urlArgs.Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getWithdrawalsPageData(firstEpoch, pageSize, tabView)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "withdrawals.go", "Withdrawals", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "withdrawals.go", "Withdrawals", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getWithdrawalsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.WithdrawalsPageData, error) {
	pageData := &models.WithdrawalsPageData{}
	pageCacheKey := fmt.Sprintf("withdrawals:%v:%v:%v", firstEpoch, pageSize, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildWithdrawalsPageData(pageCall.CallCtx, firstEpoch, pageSize, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.WithdrawalsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildWithdrawalsPageData(ctx context.Context, firstEpoch uint64, pageSize uint64, tabView string) (*models.WithdrawalsPageData, time.Duration) {
	logrus.Debugf("withdrawals page called: %v:%v:%v", firstEpoch, pageSize, tabView)
	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.WithdrawalsPageData{
		TabView: tabView,
	}

	// Compute total amount withdrawn in last 24h
	currentSlot := chainState.CurrentSlot()
	slotsPerDay := uint64(86400000 / chainState.GetSpecs().SlotDurationMs)
	minSlot24h := uint64(0)
	if uint64(currentSlot) > slotsPerDay {
		minSlot24h = uint64(currentSlot) - slotsPerDay
	}
	minBlockUid24h := minSlot24h << 16
	maxBlockUid24h := (uint64(currentSlot) + 1) << 16
	withdrawnAmount24h, _ := db.GetWithdrawalAmountSum(ctx, minBlockUid24h, maxBlockUid24h)
	pageData.WithdrawnAmount24h = withdrawnAmount24h

	// Get withdrawal request count (excluding exits) and queue stats
	oneAmount := uint64(1)
	_, _, totalWithdrawals := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, &services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			WithOrphaned: 1,
			MinAmount:    &oneAmount,
		},
	}, 0, 1)
	pageData.TotalWithdrawalCount = totalWithdrawals

	// Get withdrawal queue data
	queueFilter := &services.WithdrawalQueueFilter{}
	queuedWithdrawals, queuedWithdrawalCount, queuedAmount := services.GlobalBeaconService.GetWithdrawalQueueByFilter(ctx, queueFilter, 0, 1)
	pageData.QueuedWithdrawalCount = queuedWithdrawalCount
	pageData.WithdrawingAmount = uint64(queuedAmount)

	// Calculate queue duration estimation based on the last queued withdrawal
	if len(queuedWithdrawals) > 0 {
		lastQueueEntry := queuedWithdrawals[len(queuedWithdrawals)-1]
		pageData.QueueDurationEstimate = chainState.SlotToTime(lastQueueEntry.EstimatedWithdrawalTime)
		pageData.HasQueueDuration = true
	}

	// Only load data for the selected tab
	switch tabView {
	case "recent":
		withdrawalFilter := &services.CombinedWithdrawalRequestFilter{
			Filter: &dbtypes.WithdrawalRequestFilter{
				MinAmount:    &oneAmount,
				WithOrphaned: 1,
			},
		}

		dbWithdrawals, _, _ := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, withdrawalFilter, 0, uint32(20))
		for _, withdrawal := range dbWithdrawals {
			withdrawalData := &models.WithdrawalsPageDataRecentWithdrawal{
				SourceAddr: withdrawal.SourceAddress(),
				Amount:     withdrawal.Amount(),
				PublicKey:  withdrawal.ValidatorPubkey(),
			}

			if validatorIndex := withdrawal.ValidatorIndex(); validatorIndex != nil {
				if *validatorIndex&services.BuilderIndexFlag != 0 {
					withdrawalData.IsBuilder = true
					withdrawalData.ValidatorIndex = *validatorIndex &^ services.BuilderIndexFlag
				} else {
					withdrawalData.ValidatorIndex = *validatorIndex
				}
				withdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*validatorIndex)
				withdrawalData.ValidatorValid = true
			}

			if request := withdrawal.Request; request != nil {
				withdrawalData.IsIncluded = true
				withdrawalData.SlotNumber = request.SlotNumber
				withdrawalData.SlotRoot = request.SlotRoot
				withdrawalData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
				withdrawalData.Status = uint64(1)
				withdrawalData.Result = request.Result
				withdrawalData.ResultMessage = getWithdrawalResultMessage(request.Result, chainState.GetSpecs())
			}

			if transaction := withdrawal.Transaction; transaction != nil {
				withdrawalData.TransactionHash = transaction.TxHash
				withdrawalData.LinkedTransaction = true
				withdrawalData.TxStatus = uint64(1)
				if withdrawal.TransactionOrphaned {
					withdrawalData.TxStatus = uint64(2)
				}
			}

			pageData.RecentWithdrawals = append(pageData.RecentWithdrawals, withdrawalData)
		}
		pageData.RecentWithdrawalCount = uint64(len(pageData.RecentWithdrawals))

	case "beaconwithdrawals":
		// Load recent beacon chain withdrawals
		withdrawalFilter := &dbtypes.WithdrawalFilter{
			WithOrphaned: 1,
		}
		dbWithdrawals, _ := services.GlobalBeaconService.GetWithdrawalsByFilter(ctx, withdrawalFilter, 0, 20)

		// Batch resolve account IDs to addresses
		accountIDs := make([]uint64, 0, len(dbWithdrawals))
		validatorIDs := make([]phase0.ValidatorIndex, 0, len(dbWithdrawals))
		accountIDSet := make(map[uint64]bool, len(dbWithdrawals))
		validatorIDSet := make(map[uint64]bool, len(dbWithdrawals))
		for _, w := range dbWithdrawals {
			if w.AccountID != nil && *w.AccountID > 0 && !accountIDSet[*w.AccountID] {
				accountIDSet[*w.AccountID] = true
				accountIDs = append(accountIDs, *w.AccountID)
			} else if w.Address == nil && !validatorIDSet[w.Validator] {
				validatorIDSet[w.Validator] = true
				validatorIDs = append(validatorIDs, phase0.ValidatorIndex(w.Validator))
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

		validatorMap := make(map[uint64]v1.Validator, len(validatorIDs))
		if len(validatorIDs) > 0 {
			validatorSetRsp, _ := services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &dbtypes.ValidatorFilter{
				Indices: validatorIDs,
			}, false)

			for _, validator := range validatorSetRsp {
				validatorMap[uint64(validator.Index)] = validator
			}
		}

		// Batch resolve blocks
		blockUids := make([]uint64, 0, len(dbWithdrawals))
		blockUidSet := make(map[uint64]bool, len(dbWithdrawals))
		for _, w := range dbWithdrawals {
			if !blockUidSet[w.BlockUid] {
				blockUidSet[w.BlockUid] = true
				blockUids = append(blockUids, w.BlockUid)
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
			withdrawalData := &models.WithdrawalsPageDataBeaconWithdrawal{
				SlotNumber: slot,
				Time:       chainState.SlotToTime(phase0.Slot(slot)),
				Orphaned:   withdrawal.Orphaned,
				Type:       withdrawal.Type,
				Amount:     withdrawal.Amount,
			}

			withdrawalData.HasValidator = true
			withdrawalData.ValidatorIndex = withdrawal.Validator
			withdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(withdrawal.Validator)

			hasAddress := false
			if withdrawal.AccountID != nil {
				if acct, ok := accountMap[*withdrawal.AccountID]; ok {
					withdrawalData.Address = acct.Address
					hasAddress = true
				}
			}
			if !hasAddress && withdrawal.Address != nil {
				withdrawalData.Address = withdrawal.Address
				hasAddress = true
			}
			if !hasAddress {
				if validator, ok := validatorMap[withdrawal.Validator]; ok && (validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02) {
					withdrawalData.Address = validator.Validator.WithdrawalCredentials[12:]
					hasAddress = true
				}
			}

			if blockInfo, ok := blockMap[withdrawal.BlockUid]; ok && blockInfo.Block != nil {
				withdrawalData.BlockRoot = blockInfo.Block.Root
				if blockInfo.Block.EthBlockNumber != nil {
					withdrawalData.BlockNumber = *blockInfo.Block.EthBlockNumber
				}
			}

			pageData.BeaconWithdrawals = append(pageData.BeaconWithdrawals, withdrawalData)
		}
		pageData.BeaconWithdrawalCount = uint64(len(pageData.BeaconWithdrawals))

	case "queue":
		// Load withdrawal queue
		queueWithdrawals, _, _ := services.GlobalBeaconService.GetWithdrawalQueueByFilter(ctx, &services.WithdrawalQueueFilter{}, 0, 20)
		for _, queueEntry := range queueWithdrawals {
			queueData := &models.WithdrawalsPageDataQueuedWithdrawal{
				ValidatorIndex:    uint64(queueEntry.ValidatorIndex),
				ValidatorName:     queueEntry.ValidatorName,
				Amount:            uint64(queueEntry.Amount),
				WithdrawableEpoch: uint64(queueEntry.WithdrawableEpoch),
				PublicKey:         queueEntry.Validator.Validator.PublicKey[:],
			}

			if strings.HasPrefix(queueEntry.Validator.Status.String(), "pending") {
				queueData.ValidatorStatus = "Pending"
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveOngoing {
				queueData.ValidatorStatus = "Active"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveExiting {
				queueData.ValidatorStatus = "Exiting"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveSlashed {
				queueData.ValidatorStatus = "Slashed"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateExitedUnslashed {
				queueData.ValidatorStatus = "Exited"
			} else if queueEntry.Validator.Status == v1.ValidatorStateExitedSlashed {
				queueData.ValidatorStatus = "Slashed"
			} else {
				queueData.ValidatorStatus = queueEntry.Validator.Status.String()
			}

			if queueData.ShowUpcheck {
				queueData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(queueEntry.Validator.Index, 3))
				queueData.UpcheckMaximum = uint8(3)
			}

			// Use the calculated EstimatedWithdrawalTime from the queue entry
			queueData.EstimatedTime = chainState.SlotToTime(queueEntry.EstimatedWithdrawalTime)

			pageData.QueuedWithdrawals = append(pageData.QueuedWithdrawals, queueData)
		}
		pageData.QueuedTabCount = uint64(len(pageData.QueuedWithdrawals))
	}

	return pageData, 1 * time.Minute
}
