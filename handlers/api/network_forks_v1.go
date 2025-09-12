package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APINetworkForksResponse represents the response structure for network forks
type APINetworkForksResponse struct {
	Status string               `json:"status"`
	Data   *APINetworkForksData `json:"data"`
}

// APINetworkForksData contains the network forks data
type APINetworkForksData struct {
	ConfigName     string                `json:"config_name"`
	CurrentEpoch   uint64                `json:"current_epoch"`
	FinalizedEpoch int64                 `json:"finalized_epoch"`
	Forks          []*APINetworkForkInfo `json:"forks"`
	Count          uint64                `json:"count"`
}

// APINetworkForkInfo represents information about a single network fork
type APINetworkForkInfo struct {
	Name             string  `json:"name"`
	Version          *string `json:"version,omitempty"` // nil for BPO forks
	Epoch            uint64  `json:"epoch"`
	Active           bool    `json:"active"`
	Scheduled        bool    `json:"scheduled"`
	Time             int64   `json:"time,omitempty"`
	Type             string  `json:"type"` // "consensus" or "bpo"
	ForkDigest       string  `json:"fork_digest"`
	MaxBlobsPerBlock *uint64 `json:"max_blobs_per_block,omitempty"` // only for BPO forks
}

// APINetworkForksV1 returns information about network forks including BPO forks and fork digests
// @Summary Get network forks
// @Description Returns comprehensive information about past, current, and future network forks including consensus forks and BPO (Block Parameter Override) forks with fork digests
// @Tags network
// @Accept json
// @Produce json
// @Success 200 {object} APINetworkForksResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/network/forks [get]
func APINetworkForksV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	networkGenesis := chainState.GetGenesis()

	if specs == nil {
		http.Error(w, `{"status": "ERROR: chain specs not available"}`, http.StatusInternalServerError)
		return
	}

	var forks []*APINetworkForkInfo

	// Add Phase0 (Genesis) fork
	if networkGenesis != nil {
		forkDigest := chainState.GetForkDigest(phase0.Version(networkGenesis.GenesisForkVersion), nil)
		version := fmt.Sprintf("0x%x", networkGenesis.GenesisForkVersion)
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Phase0",
			Version:    &version,
			Epoch:      0,
			Active:     true,
			Scheduled:  true,
			Time:       networkGenesis.GenesisTime.Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Altair fork
	if specs.AltairForkEpoch != nil && *specs.AltairForkEpoch < uint64(18446744073709551615) {
		forkDigest := chainState.GetForkDigest(specs.AltairForkVersion, nil)
		version := fmt.Sprintf("0x%x", specs.AltairForkVersion)
		epoch := *specs.AltairForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Altair",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Bellatrix fork
	if specs.BellatrixForkEpoch != nil && *specs.BellatrixForkEpoch < uint64(18446744073709551615) {
		forkDigest := chainState.GetForkDigest(specs.BellatrixForkVersion, nil)
		version := fmt.Sprintf("0x%x", specs.BellatrixForkVersion)
		epoch := *specs.BellatrixForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Bellatrix",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Capella fork
	if specs.CapellaForkEpoch != nil && *specs.CapellaForkEpoch < uint64(18446744073709551615) {
		forkDigest := chainState.GetForkDigest(specs.CapellaForkVersion, nil)
		version := fmt.Sprintf("0x%x", specs.CapellaForkVersion)
		epoch := *specs.CapellaForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Capella",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Deneb fork
	if specs.DenebForkEpoch != nil && *specs.DenebForkEpoch < uint64(18446744073709551615) {
		forkDigest := chainState.GetForkDigest(specs.DenebForkVersion, nil)
		version := fmt.Sprintf("0x%x", specs.DenebForkVersion)
		epoch := *specs.DenebForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Deneb",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Electra fork
	if specs.ElectraForkEpoch != nil && *specs.ElectraForkEpoch < uint64(18446744073709551615) {
		forkDigest := chainState.GetForkDigest(specs.ElectraForkVersion, nil)
		version := fmt.Sprintf("0x%x", specs.ElectraForkVersion)
		epoch := *specs.ElectraForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Electra",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add Fulu fork
	if specs.FuluForkEpoch != nil && *specs.FuluForkEpoch < uint64(18446744073709551615) {
		currentBlobParams := &consensus.BlobScheduleEntry{
			Epoch:            *specs.ElectraForkEpoch,
			MaxBlobsPerBlock: specs.MaxBlobsPerBlockElectra,
		}
		forkDigest := chainState.GetForkDigest(specs.FuluForkVersion, currentBlobParams)
		version := fmt.Sprintf("0x%x", specs.FuluForkVersion)
		epoch := *specs.FuluForkEpoch
		forks = append(forks, &APINetworkForkInfo{
			Name:       "Fulu",
			Version:    &version,
			Epoch:      epoch,
			Active:     uint64(currentEpoch) >= epoch,
			Scheduled:  true,
			Time:       chainState.EpochToTime(phase0.Epoch(epoch)).Unix(),
			Type:       "consensus",
			ForkDigest: fmt.Sprintf("0x%x", forkDigest),
		})
	}

	// Add BPO forks from BLOB_SCHEDULE
	for i, blobSchedule := range specs.BlobSchedule {
		// BPO forks use the fork version that's active at the time of BPO activation
		forkVersion := chainState.GetForkVersionAtEpoch(phase0.Epoch(blobSchedule.Epoch))
		blobParams := &consensus.BlobScheduleEntry{
			Epoch:            blobSchedule.Epoch,
			MaxBlobsPerBlock: blobSchedule.MaxBlobsPerBlock,
		}
		forkDigest := chainState.GetForkDigest(forkVersion, blobParams)

		forks = append(forks, &APINetworkForkInfo{
			Name:             fmt.Sprintf("BPO%d", i+1),
			Version:          nil, // BPO forks don't have fork versions
			Epoch:            blobSchedule.Epoch,
			Active:           uint64(currentEpoch) >= blobSchedule.Epoch,
			Scheduled:        true,
			Time:             chainState.EpochToTime(phase0.Epoch(blobSchedule.Epoch)).Unix(),
			Type:             "bpo",
			ForkDigest:       fmt.Sprintf("0x%x", forkDigest),
			MaxBlobsPerBlock: &blobSchedule.MaxBlobsPerBlock,
		})
	}

	// Sort all forks by epoch
	sort.Slice(forks, func(i, j int) bool {
		return forks[i].Epoch < forks[j].Epoch
	})

	response := APINetworkForksResponse{
		Status: "OK",
		Data: &APINetworkForksData{
			ConfigName:     specs.ConfigName,
			CurrentEpoch:   uint64(currentEpoch),
			FinalizedEpoch: int64(uint64(finalizedEpoch)),
			Forks:          forks,
			Count:          uint64(len(forks)),
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode network forks response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
