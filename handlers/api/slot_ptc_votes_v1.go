package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotPtcVotesResponse represents the response for the slot PTC votes endpoint.
type APISlotPtcVotesResponse struct {
	Status string               `json:"status"`
	Data   *APISlotPtcVotesData `json:"data"`
}

// APISlotPtcVotesData represents the PTC vote summary for a slot.
type APISlotPtcVotesData struct {
	Slot            uint64                 `json:"slot"`
	BlockRoot       string                 `json:"block_root"`
	VotedSlot       uint64                 `json:"voted_slot"`
	VotedBlockRoot  string                 `json:"voted_block_root,omitempty"`
	TotalPtcSize    uint64                 `json:"total_ptc_size"`
	VoteCount       uint64                 `json:"vote_count"`
	NonVoterCount   uint64                 `json:"non_voter_count"`
	NonVoterPercent float64                `json:"non_voter_percent"`
	Participation   float64                `json:"participation"`
	Aggregates      []*APISlotPtcAggregate `json:"aggregates"`
	NonVoters       []APISlotPtcValidator  `json:"non_voters"`
}

// APISlotPtcAggregate represents a single PTC payload-attestation aggregate.
type APISlotPtcAggregate struct {
	PayloadPresent    bool                  `json:"payload_present"`
	BlobDataAvailable bool                  `json:"blob_data_available"`
	AggregationBits   string                `json:"aggregation_bits"`
	Signature         string                `json:"signature"`
	VoteCount         uint64                `json:"vote_count"`
	VotePercent       float64               `json:"vote_percent"`
	Validators        []APISlotPtcValidator `json:"validators"`
}

// APISlotPtcValidator is a named validator entry.
type APISlotPtcValidator struct {
	Index uint64 `json:"index"`
	Name  string `json:"name,omitempty"`
}

// APISlotPtcVotesV1 returns the PTC (Payload Timeliness Committee) votes contained in a slot.
// @Summary Get PTC votes for a slot
// @Description Returns the PTC (Payload Timeliness Committee) payload attestations included in a gloas+ block.
// @Description The attestations target the previous slot, mirroring what is shown on the slot page.
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotPtcVotesResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/ptc_votes [get]
// @ID getSlotPtcVotes
func APISlotPtcVotesV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Resolve through the indexer first so unknown slots/roots return 404
	// instead of bubbling up a 500 from beacon header loading.
	dbSlot := resolveSlotOrHash(r.Context(), w, mux.Vars(r)["slotOrHash"])
	if dbSlot == nil {
		return
	}

	var resolvedRoot phase0.Root
	copy(resolvedRoot[:], dbSlot.Root)
	resolvedSlot := phase0.Slot(dbSlot.Slot)

	data := &APISlotPtcVotesData{
		Slot:       uint64(resolvedSlot),
		BlockRoot:  fmt.Sprintf("0x%x", dbSlot.Root),
		Aggregates: []*APISlotPtcAggregate{},
		NonVoters:  []APISlotPtcValidator{},
	}

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), resolvedRoot)
	if err != nil {
		logrus.WithError(err).Error("failed to load slot details")
		http.Error(w, `{"status": "ERROR: failed to load slot details"}`, http.StatusInternalServerError)
		return
	}
	if blockData == nil || blockData.Block == nil {
		writePtcVotesResponse(w, data)
		return
	}

	if blockData.Block.Version < spec.DataVersionGloas {
		writePtcVotesResponse(w, data)
		return
	}

	if blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		writePtcVotesResponse(w, data)
		return
	}

	payloadAttestations := blockData.Block.Message.Body.PayloadAttestations
	if len(payloadAttestations) == 0 {
		writePtcVotesResponse(w, data)
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	votedSlot := resolvedSlot - 1
	votedEpoch := chainState.EpochOfSlot(votedSlot)
	data.VotedSlot = uint64(votedSlot)
	data.TotalPtcSize = specs.PtcSize

	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	epochStats := beaconIndexer.GetEpochStatsByEpoch(votedEpoch)
	var ptcDuties []phase0.ValidatorIndex
	for _, es := range epochStats {
		values := es.GetValues(true)
		if values == nil || values.PtcDuties == nil {
			continue
		}
		slotInEpoch := uint64(votedSlot) % specs.SlotsPerEpoch
		if slotInEpoch >= uint64(len(values.PtcDuties)) || values.PtcDuties[slotInEpoch] == nil {
			continue
		}
		ptcDuties = make([]phase0.ValidatorIndex, len(values.PtcDuties[slotInEpoch]))
		for i, activeIdx := range values.PtcDuties[slotInEpoch] {
			if int(activeIdx) < len(values.ActiveIndices) {
				ptcDuties[i] = values.ActiveIndices[activeIdx]
			}
		}
		break
	}

	votedPositions := make(map[uint64]bool, specs.PtcSize)
	totalVotes := uint64(0)

	for _, pa := range payloadAttestations {
		if pa == nil || pa.Data == nil {
			continue
		}
		if data.VotedBlockRoot == "" {
			data.VotedBlockRoot = fmt.Sprintf("0x%x", pa.Data.BeaconBlockRoot[:])
		}

		aggregate := &APISlotPtcAggregate{
			PayloadPresent:    pa.Data.PayloadPresent,
			BlobDataAvailable: pa.Data.BlobDataAvailable,
			AggregationBits:   fmt.Sprintf("0x%x", pa.AggregationBits),
			Signature:         fmt.Sprintf("0x%x", pa.Signature[:]),
			Validators:        []APISlotPtcValidator{},
		}

		bitCount := uint64(len(pa.AggregationBits)) * 8
		if bitCount > specs.PtcSize {
			bitCount = specs.PtcSize
		}
		seenValidators := make(map[uint64]bool)
		bitVoteCount := uint64(0)
		for i := uint64(0); i < bitCount; i++ {
			if (pa.AggregationBits[i/8]>>(i%8))&1 != 1 {
				continue
			}
			votedPositions[i] = true
			bitVoteCount++
			if int(i) >= len(ptcDuties) {
				continue
			}
			vidx := uint64(ptcDuties[i])
			if seenValidators[vidx] {
				continue
			}
			seenValidators[vidx] = true
			aggregate.Validators = append(aggregate.Validators, APISlotPtcValidator{
				Index: vidx,
				Name:  services.GlobalBeaconService.GetValidatorName(vidx),
			})
		}
		aggregate.VoteCount = bitVoteCount
		totalVotes += bitVoteCount

		data.Aggregates = append(data.Aggregates, aggregate)
	}

	voterSet := make(map[uint64]bool)
	if len(ptcDuties) > 0 {
		for i, vidx := range ptcDuties {
			if votedPositions[uint64(i)] {
				voterSet[uint64(vidx)] = true
			}
		}
		nonVoterSet := make(map[uint64]bool)
		for _, vidx := range ptcDuties {
			v := uint64(vidx)
			if !voterSet[v] {
				nonVoterSet[v] = true
			}
		}
		nonVoters := make([]APISlotPtcValidator, 0, len(nonVoterSet))
		for vidx := range nonVoterSet {
			nonVoters = append(nonVoters, APISlotPtcValidator{
				Index: vidx,
				Name:  services.GlobalBeaconService.GetValidatorName(vidx),
			})
		}
		data.NonVoters = nonVoters
		data.NonVoterCount = uint64(len(nonVoters))
	}

	totalUniqueValidators := uint64(len(voterSet)) + data.NonVoterCount
	switch {
	case totalUniqueValidators > 0:
		data.TotalPtcSize = totalUniqueValidators
		data.Participation = float64(len(voterSet)) / float64(totalUniqueValidators)
		data.NonVoterPercent = float64(data.NonVoterCount) / float64(totalUniqueValidators) * 100
		for _, agg := range data.Aggregates {
			agg.VotePercent = float64(agg.VoteCount) / float64(totalUniqueValidators) * 100
		}
	case specs.PtcSize > 0:
		totalVoted := uint64(len(votedPositions))
		data.NonVoterCount = specs.PtcSize - totalVoted
		data.Participation = float64(totalVoted) / float64(specs.PtcSize)
		data.NonVoterPercent = float64(data.NonVoterCount) / float64(specs.PtcSize) * 100
		for _, agg := range data.Aggregates {
			agg.VotePercent = float64(agg.VoteCount) / float64(specs.PtcSize) * 100
		}
	}
	data.VoteCount = totalVotes

	writePtcVotesResponse(w, data)
}

func writePtcVotesResponse(w http.ResponseWriter, data *APISlotPtcVotesData) {
	resp := APISlotPtcVotesResponse{Status: "OK", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode PTC votes response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
