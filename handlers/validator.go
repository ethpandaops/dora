package handlers

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

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

			for validatorActivityIdx < len(validatorActivity) && chainState.EpochOfSlot(validatorActivity[validatorActivityIdx].VoteBlock.Slot) == epoch {
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

					if attestation.Epoch+1 >= currentEpoch {
						attestation.Scheduled = true
					}
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

	}

	return pageData, 10 * time.Minute
}
