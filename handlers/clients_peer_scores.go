package handlers

import (
	"net/http"
	"sort"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// ClientsPeerScores serves the /clients/peer_scores page. The handler is
// registered only when PeerScoresConfig.Enabled is true.
func ClientsPeerScores(w http.ResponseWriter, r *http.Request) {
	clientsTemplateFiles := append(layoutTemplateFiles,
		"clients/clients_peer_scores.html",
	)

	pageTemplate := templates.GetTemplate(clientsTemplateFiles...)
	data := InitPageData(w, r, "clients/peer_scores", "/clients/peer_scores", "Peer scores", clientsTemplateFiles)

	pageError := services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getPeerScoresPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "clients_peer_scores.go", "Peer scores", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getPeerScoresPageData() (*models.ClientsPeerScoresPageData, error) {
	pageData := &models.ClientsPeerScoresPageData{}
	pageCacheKey := "clients/peer_scores"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		built, cacheTimeout := buildPeerScoresPageData()
		pageCall.CacheTimeout = cacheTimeout
		return built
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ClientsPeerScoresPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildPeerScoresPageData() (*models.ClientsPeerScoresPageData, time.Duration) {
	pageData := &models.ClientsPeerScoresPageData{
		Matrix:          make(map[string]map[string]*models.PeerScoresCell, 8),
		Reporters:       make([]*models.PeerScoresReporter, 0, 8),
		Targets:         make([]*models.PeerScoresTarget, 0, 64),
		ReasonHistogram: make([]*models.PeerScoresReasonCount, 0, 16),
		ScrapedAtMs:     time.Now().UnixMilli(),
	}

	if utils.Config.PeerScores != nil {
		pageData.PollIntervalSeconds = utils.Config.PeerScores.PollIntervalSeconds
	}

	clients := services.GlobalBeaconService.GetConsensusClients()
	if len(clients) == 0 {
		return pageData, 5 * time.Second
	}

	// First pass: build the reporter list, indexed by peer ID. We use
	// that index later to mark targets that happen to be one of our
	// own reporters.
	reporterByPeerID := make(map[string]*models.PeerScoresReporter, len(clients))
	scoresByReporterPeerID := make(map[string][]*rpc.PeerScore, len(clients))

	for _, client := range clients {
		identity := client.GetNodeIdentity()
		peerID := ""
		if identity != nil {
			peerID = identity.PeerID
		}

		reporter := &models.PeerScoresReporter{
			PeerID:     peerID,
			Name:       client.GetName(),
			ClientType: int8(client.GetClientType()),
			ClientName: client.GetClientType().String(),
			Supported:  !client.IsPeerScoresUnsupported(),
			FetchedAt:  client.GetPeerScoresFetchedAt().UnixMilli(),
		}
		pageData.Reporters = append(pageData.Reporters, reporter)

		if peerID != "" {
			reporterByPeerID[peerID] = reporter
		}
		if peerID != "" {
			scoresByReporterPeerID[peerID] = client.GetPeerScores()
		}
	}

	// Stable reporter ordering: sort by name so columns/rows don't
	// shuffle between refreshes.
	sort.Slice(pageData.Reporters, func(i, j int) bool {
		return pageData.Reporters[i].Name < pageData.Reporters[j].Name
	})

	// Second pass: walk every reporter's snapshot to discover targets
	// and populate the matrix.
	targetByPeerID := make(map[string]*models.PeerScoresTarget, 64)
	reasonCounts := make(map[string]int, 16)

	for reporterPeerID, scores := range scoresByReporterPeerID {
		row := make(map[string]*models.PeerScoresCell, len(scores))

		for _, score := range scores {
			if score == nil || score.PeerID == "" {
				continue
			}

			target, ok := targetByPeerID[score.PeerID]
			if !ok {
				target = &models.PeerScoresTarget{
					PeerID:      score.PeerID,
					PeerIDShort: shortPeerID(score.PeerID),
				}
				if r := reporterByPeerID[score.PeerID]; r != nil {
					target.KnownClientType = r.ClientType
					target.KnownAs = r.Name
				}
				targetByPeerID[score.PeerID] = target
			}
			if score.AgentVersion != "" && target.AgentVersion == "" {
				target.AgentVersion = score.AgentVersion
			}

			cell := &models.PeerScoresCell{
				Score:           score.Score,
				ScoreNormalized: score.ScoreNormalized,
				ScoreState:      score.ScoreState,
				Components:      flattenComponents(score.Components),
			}
			if score.LastEvent != nil {
				cell.LastEventReason = score.LastEvent.Reason
				cell.LastEventNative = score.LastEvent.NativeReason
				cell.LastEventSecondsAgo = score.LastEvent.SecondsAgo
				if cell.LastEventReason != "" {
					reasonCounts[cell.LastEventReason]++
				}
			}

			row[score.PeerID] = cell
		}

		pageData.Matrix[reporterPeerID] = row
	}

	pageData.Targets = make([]*models.PeerScoresTarget, 0, len(targetByPeerID))
	for _, target := range targetByPeerID {
		pageData.Targets = append(pageData.Targets, target)
	}
	sort.Slice(pageData.Targets, func(i, j int) bool {
		ai, aj := pageData.Targets[i], pageData.Targets[j]
		if (ai.KnownClientType > 0) != (aj.KnownClientType > 0) {
			return ai.KnownClientType > 0
		}
		return ai.PeerID < aj.PeerID
	})

	for reason, count := range reasonCounts {
		pageData.ReasonHistogram = append(pageData.ReasonHistogram, &models.PeerScoresReasonCount{
			Reason:   reason,
			Category: rpc.CategoryFor(reason),
			Count:    count,
		})
	}
	sort.Slice(pageData.ReasonHistogram, func(i, j int) bool {
		if pageData.ReasonHistogram[i].Count != pageData.ReasonHistogram[j].Count {
			return pageData.ReasonHistogram[i].Count > pageData.ReasonHistogram[j].Count
		}
		return pageData.ReasonHistogram[i].Reason < pageData.ReasonHistogram[j].Reason
	})

	cacheTimeout := 5 * time.Second
	if utils.Config.PeerScores != nil && utils.Config.PeerScores.PollIntervalSeconds > 0 {
		cacheTimeout = time.Duration(utils.Config.PeerScores.PollIntervalSeconds) * time.Second / 2
		if cacheTimeout < 2*time.Second {
			cacheTimeout = 2 * time.Second
		}
	}
	return pageData, cacheTimeout
}

// shortPeerID returns a compact 12-char-ish display form of a libp2p
// peer ID. Falls back to the full string if it's shorter.
func shortPeerID(peerID string) string {
	if len(peerID) <= 12 {
		return peerID
	}
	return peerID[:6] + "..." + peerID[len(peerID)-4:]
}

// flattenComponents converts the optional pointer-keyed component
// struct into a string→float64 map for the template. Nil entries are
// dropped.
func flattenComponents(c rpc.PeerScoreComponents) map[string]float64 {
	out := make(map[string]float64, 6)
	if c.Gossipsub != nil {
		out["gossipsub"] = *c.Gossipsub
	}
	if c.Reputation != nil {
		out["reputation"] = *c.Reputation
	}
	if c.BadResponses != nil {
		out["bad_responses"] = *c.BadResponses
	}
	if c.PeerStatus != nil {
		out["peer_status"] = *c.PeerStatus
	}
	if c.BlockProvider != nil {
		out["block_provider"] = *c.BlockProvider
	}
	if c.BehaviourPenalty != nil {
		out["behaviour_penalty"] = *c.BehaviourPenalty
	}
	return out
}

// PeerScoresPageData export of buildPeerScoresPageData for the API
// handler so it can return the same snapshot the page renders. Bypasses
// the frontend cache.
func PeerScoresPageData() *models.ClientsPeerScoresPageData {
	data, _ := buildPeerScoresPageData()
	return data
}

// PeerScoresLookupReporter returns the reporter peer ID -> human name
// map for the API to translate query parameters that pass a friendly
// reporter name instead of a peer ID.
func PeerScoresLookupReporter(query string) *consensus.Client {
	if query == "" {
		return nil
	}
	for _, client := range services.GlobalBeaconService.GetConsensusClients() {
		if id := client.GetNodeIdentity(); id != nil && id.PeerID == query {
			return client
		}
		if client.GetName() == query {
			return client
		}
	}
	return nil
}
