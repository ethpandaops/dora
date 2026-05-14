package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotSupported is returned by GetPeerScores when the connected
// consensus client does not expose a peer-scores endpoint (e.g.
// stock Nimbus / Grandine / Caplin).
var ErrNotSupported = errors.New("peer scores not supported by this client")

// PeerScoreEvent captures a single scoring event observed by the
// reporter for a target peer. The vocabulary in Reason is controlled
// (see peerscores_reasons.go); NativeReason preserves the original
// client-side string for fidelity.
type PeerScoreEvent struct {
	Reason       string  `json:"reason"`
	NativeReason string  `json:"native_reason"`
	Direction    string  `json:"direction,omitempty"`
	Delta        float64 `json:"delta,omitempty"`
	Topic        string  `json:"topic,omitempty"`
	SecondsAgo   uint64  `json:"seconds_ago,omitempty"`
}

// PeerScoreComponents holds the per-subsystem score components the
// underlying client exposes. All fields are optional - clients vary
// widely in what they report.
type PeerScoreComponents struct {
	Gossipsub        *float64 `json:"gossipsub,omitempty"`
	Reputation       *float64 `json:"reputation,omitempty"`
	BadResponses     *float64 `json:"bad_responses,omitempty"`
	PeerStatus       *float64 `json:"peer_status,omitempty"`
	BlockProvider    *float64 `json:"block_provider,omitempty"`
	BehaviourPenalty *float64 `json:"behaviour_penalty,omitempty"`
}

// PeerScore is the normalized per-peer scoring snapshot returned by
// each per-client fetcher. Score* fields preserve the client-native
// range; ScoreNormalized maps to [-1, +1] for cross-client display.
type PeerScore struct {
	PeerID          string              `json:"peer_id"`
	State           string              `json:"state,omitempty"`
	Direction       string              `json:"direction,omitempty"`
	Score           float64             `json:"score"`
	ScoreNormalized float64             `json:"score_normalized"`
	ScoreMin        float64             `json:"score_min"`
	ScoreMax        float64             `json:"score_max"`
	ScoreState      string              `json:"score_state"`
	Components      PeerScoreComponents `json:"components"`
	LastEvent       *PeerScoreEvent     `json:"last_event,omitempty"`
	LastDisconnect  *PeerScoreEvent     `json:"last_disconnect,omitempty"`
	AgentVersion    string              `json:"agent_version,omitempty"`
	FetchedAt       int64               `json:"fetched_at"`
}

// clamp confines v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalizeSymmetric maps a symmetric (min == -max) range to [-1, +1]
// with clamping.
func normalizeSymmetric(score, min, max float64) float64 {
	if max == min {
		return 0
	}
	n := (score-min)/(max-min)*2 - 1
	return clamp(n, -1, 1)
}

// normalizeAroundNeutral maps an asymmetric range with a known neutral
// anchor to [-1, +1]. score == neutral always returns 0 so a fresh,
// healthy peer never falls into the negative state bands even when the
// range itself is asymmetric (e.g. Teku's [-10, +20] with neutral=0).
func normalizeAroundNeutral(score, min, max, neutral float64) float64 {
	switch {
	case score >= neutral:
		if max == neutral {
			return 0
		}
		return clamp((score-neutral)/(max-neutral), 0, 1)
	default:
		if neutral == min {
			return 0
		}
		return clamp((score-neutral)/(neutral-min), -1, 0)
	}
}

// normalizePrysm maps Prysm's asymmetric range (min=-100, max=+1) to
// [-1, +1]. The native max of +1 is treated as +1 so even a perfectly
// healthy Prysm peer reads as the top of the band.
func normalizePrysm(score float64) float64 {
	if score >= 1 {
		return 1
	}
	if score <= -100 {
		return -1
	}
	// Map [-100, 1] to [-1, 1] linearly.
	return (score-(-100))/(1-(-100))*2 - 1
}

// scoreStateFor returns a coarse health label given a normalized score.
// Bands: healthy (>= 0), warning (any negative but above disconnect),
// disconnect (-20/100 = -0.2), banned (-50/100 = -0.5). Warning is the
// "the peer is being penalized but the client hasn't acted yet" tier
// so any negative score is visually distinct from a healthy 0.
func scoreStateFor(scoreNormalized float64) string {
	switch {
	case scoreNormalized <= -0.5:
		return "banned"
	case scoreNormalized <= -0.2:
		return "disconnect"
	case scoreNormalized < 0:
		return "warning"
	default:
		return "healthy"
	}
}

// nowMs returns the current wall-clock time in unix milliseconds.
func nowMs() int64 {
	return time.Now().UnixMilli()
}

// synthesizeLastEvent infers a reason when the peer's score state is bad
// but the underlying client never produced an explicit last-action tag.
// Gossipsub-driven bans are the most common case: libp2p computes the
// gossipsub score internally and the eth2 client never routes that
// through its downscore-with-reason machinery, so dora has to interpret
// the components itself. The synthesized NativeReason carries the value
// that drove it so the user can see the magnitude, not just the label.
func synthesizeLastEvent(ps *PeerScore) {
	if ps.LastEvent != nil || ps.ScoreState == "healthy" {
		return
	}
	worst := ""
	worstVal := 0.0
	if g := ps.Components.Gossipsub; g != nil && *g < -100 {
		worst, worstVal = ReasonGossipsubLow, *g
	}
	if b := ps.Components.BadResponses; b != nil && *b < -10 && *b < worstVal {
		worst, worstVal = ReasonBadResponsesAccumulated, *b
	}
	if s := ps.Components.PeerStatus; s != nil && *s <= 0 && worst == "" {
		worst, worstVal = ReasonPeerStatusFailed, *s
	}
	if worst == "" {
		return
	}
	ps.LastEvent = &PeerScoreEvent{
		Reason:       worst,
		NativeReason: fmt.Sprintf("%s=%.2f (inferred)", worst, worstVal),
	}
}

// --- Lighthouse -------------------------------------------------------------

// lhScore matches Lighthouse's externally-tagged Score enum: trusted peers
// serialize as the bare string "Max"; everyone else as {"Real": {...}}.
type lhScore struct {
	Trusted bool
	Real    *struct {
		LighthouseScore              float64 `json:"lighthouse_score"`
		GossipsubScore               float64 `json:"gossipsub_score"`
		IgnoreNegativeGossipsubScore bool    `json:"ignore_negative_gossipsub_score"`
		Score                        float64 `json:"score"`
	} `json:"Real,omitempty"`
}

func (s *lhScore) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s.Trusted = str == "Max"
		return nil
	}
	type alias lhScore
	return json.Unmarshal(data, (*alias)(s))
}

type lhPeersResponse []struct {
	PeerID   string `json:"peer_id"`
	PeerInfo struct {
		Client struct {
			Kind        string `json:"kind"`
			Version     string `json:"version"`
			AgentString string `json:"agent_string"`
		} `json:"client"`
		ConnectionDirection string `json:"connection_direction"`
		ConnectionStatus    struct {
			Status string `json:"status"`
		} `json:"connection_status"`
		Score      lhScore `json:"score"`
		LastAction *struct {
			Reason     string  `json:"reason"`
			Source     string  `json:"source"`
			Action     string  `json:"action"`
			Delta      float64 `json:"delta"`
			SecondsAgo uint64  `json:"seconds_ago"`
		} `json:"last_action"`
		LastDisconnect *struct {
			Reason     string `json:"reason"`
			Code       int    `json:"code"`
			Direction  string `json:"direction"`
			SecondsAgo uint64 `json:"seconds_ago"`
		} `json:"last_disconnect"`
	} `json:"peer_info"`
}

// GetLighthousePeerScores polls Lighthouse's /lighthouse/peers endpoint
// and returns per-peer scores in the normalized PeerScore shape.
func (bc *BeaconClient) GetLighthousePeerScores(ctx context.Context) ([]*PeerScore, error) {
	var resp lhPeersResponse
	url := fmt.Sprintf("%s/lighthouse/peers", bc.endpoint)
	if err := bc.getJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("lighthouse peer scores: %w", err)
	}

	const lhMin, lhMax = -100.0, 100.0
	out := make([]*PeerScore, 0, len(resp))
	fetched := nowMs()
	for _, item := range resp {
		var (
			score         float64
			gossip        float64
			lhScoreVal    float64
			hasComponents bool
		)
		if r := item.PeerInfo.Score.Real; r != nil {
			score = r.Score
			gossip = r.GossipsubScore
			lhScoreVal = r.LighthouseScore
			hasComponents = true
		} else if item.PeerInfo.Score.Trusted {
			score = lhMax
		}
		ps := &PeerScore{
			PeerID:          item.PeerID,
			State:           item.PeerInfo.ConnectionStatus.Status,
			Direction:       strings.ToLower(item.PeerInfo.ConnectionDirection),
			Score:           score,
			ScoreNormalized: normalizeSymmetric(score, lhMin, lhMax),
			ScoreMin:        lhMin,
			ScoreMax:        lhMax,
			AgentVersion:    item.PeerInfo.Client.AgentString,
			FetchedAt:       fetched,
		}
		ps.ScoreState = scoreStateFor(ps.ScoreNormalized)
		if hasComponents {
			g, rep := gossip, lhScoreVal
			ps.Components.Gossipsub = &g
			ps.Components.Reputation = &rep
		}

		if a := item.PeerInfo.LastAction; a != nil {
			ps.LastEvent = &PeerScoreEvent{
				Reason:       translateLighthouseReason(a.Reason),
				NativeReason: a.Reason,
				Direction:    ps.Direction,
				Delta:        a.Delta,
				SecondsAgo:   a.SecondsAgo,
			}
		}
		if d := item.PeerInfo.LastDisconnect; d != nil {
			ps.LastDisconnect = &PeerScoreEvent{
				Reason:       translateLighthouseReason(d.Reason),
				NativeReason: d.Reason,
				Direction:    strings.ToLower(d.Direction),
				SecondsAgo:   d.SecondsAgo,
			}
		}
		synthesizeLastEvent(ps)
		out = append(out, ps)
	}
	return out, nil
}

// --- Lodestar ---------------------------------------------------------------

type lodestarPeersResponse struct {
	Data []struct {
		PeerID                    string   `json:"peer_id"`
		LodestarScore             float64  `json:"lodestar_score"`
		GossipScore               float64  `json:"gossip_score"`
		IgnoreNegativeGossipScore bool     `json:"ignore_negative_gossip_score"`
		Score                     float64  `json:"score"`
		LastUpdate                int64    `json:"last_update"`
		LastActionName            *string  `json:"last_action_name"`
		LastActionDeltaScore      *float64 `json:"last_action_delta_score"`
		LastActionUnixMs          *int64   `json:"last_action_unix_ms"`
	} `json:"data"`
}

// GetLodestarPeerScores polls Lodestar's
// /eth/v1/lodestar/lodestar_peer_score_stats endpoint.
func (bc *BeaconClient) GetLodestarPeerScores(ctx context.Context) ([]*PeerScore, error) {
	var resp lodestarPeersResponse
	url := fmt.Sprintf("%s/eth/v1/lodestar/lodestar_peer_score_stats", bc.endpoint)
	if err := bc.getJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("lodestar peer scores: %w", err)
	}

	const lsMin, lsMax = -100.0, 100.0
	// Lodestar's `score` field is only recomputed when reportPeer() fires.
	// Pure gossipsub updates leave it stale, so dora blends locally using
	// Lodestar's own formula: score = lodestar_score + gossip_score *
	// (MIN_SCORE_BEFORE_DISCONNECT + 1) / graylistThreshold = -19/-16000.
	const lsGossipWeight = 19.0 / 16000.0
	out := make([]*PeerScore, 0, len(resp.Data))
	fetched := nowMs()
	for _, item := range resp.Data {
		score := item.LodestarScore
		if score > -60 {
			if item.GossipScore >= 0 {
				score += item.GossipScore * lsGossipWeight
			} else if !item.IgnoreNegativeGossipScore {
				score += item.GossipScore * lsGossipWeight
			}
		}
		ps := &PeerScore{
			PeerID:          item.PeerID,
			Score:           score,
			ScoreNormalized: normalizeSymmetric(score, lsMin, lsMax),
			ScoreMin:        lsMin,
			ScoreMax:        lsMax,
			FetchedAt:       fetched,
		}
		ps.ScoreState = scoreStateFor(ps.ScoreNormalized)

		gossip := item.GossipScore
		rep := item.LodestarScore
		ps.Components.Gossipsub = &gossip
		ps.Components.Reputation = &rep

		if item.LastActionName != nil {
			native := *item.LastActionName
			delta := 0.0
			if item.LastActionDeltaScore != nil {
				delta = *item.LastActionDeltaScore
			}
			var secondsAgo uint64
			if item.LastActionUnixMs != nil {
				if diff := time.Now().UnixMilli() - *item.LastActionUnixMs; diff > 0 {
					secondsAgo = uint64(diff / 1000)
				}
			}
			ps.LastEvent = &PeerScoreEvent{
				Reason:       translateLodestarReason(native),
				NativeReason: native,
				Delta:        delta,
				SecondsAgo:   secondsAgo,
			}
		}
		synthesizeLastEvent(ps)
		out = append(out, ps)
	}
	return out, nil
}

// --- Standard /eth/v1/node/peers --------------------------------------------

// standardPeer mirrors the simplified beacon-API spec extension to
// /eth/v1/node/peers: agent_version, score, disconnect_reason,
// downscore_reasons are all optional additive fields.
type standardPeer struct {
	PeerID             string `json:"peer_id"`
	State              string `json:"state"`
	Direction          string `json:"direction"`
	LastSeenP2PAddress string `json:"last_seen_p2p_address"`
	AgentVersion       string `json:"agent_version,omitempty"`
	// Agent is Nimbus's existing field name (pre-spec); falls back when
	// agent_version is empty.
	Agent            string   `json:"agent,omitempty"`
	Score            *float64 `json:"score,omitempty"`
	DisconnectReason string   `json:"disconnect_reason,omitempty"`
	DownscoreReasons []string `json:"downscore_reasons,omitempty"`
}

type standardPeersResponse struct {
	Data []standardPeer `json:"data"`
}

// rangeForAgent picks a sensible (min, max, neutral) score range from
// the peer's libp2p agent string. The spec leaves score scale
// implementation-defined, so dora keeps per-client knowledge to drive
// normalized bars.
func rangeForAgent(agent string) (min, max, neutral float64) {
	lower := strings.ToLower(agent)
	switch {
	case strings.Contains(lower, "teku"):
		return -10, 20, 0
	case strings.Contains(lower, "prysm"):
		return -100, 1, 0
	case strings.Contains(lower, "nimbus"):
		return 0, 1000, 300
	default:
		return -100, 100, 0
	}
}

// GetStandardPeerScores polls /eth/v1/node/peers and maps the spec-
// extended Peer records to PeerScore. ErrNotSupported is returned when
// none of the observed peers populated the new score fields - the
// caller can then fall back to a per-client native endpoint.
func (bc *BeaconClient) GetStandardPeerScores(ctx context.Context) ([]*PeerScore, error) {
	var resp standardPeersResponse
	url := fmt.Sprintf("%s/eth/v1/node/peers", bc.endpoint)
	if err := bc.getJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("standard peer scores: %w", err)
	}

	out := make([]*PeerScore, 0, len(resp.Data))
	fetched := nowMs()
	scoreSeen := false
	for _, p := range resp.Data {
		if p.PeerID == "" {
			continue
		}
		agentVersion := p.AgentVersion
		if agentVersion == "" {
			agentVersion = p.Agent
		}
		min, max, neutral := rangeForAgent(agentVersion)
		var score float64
		if p.Score != nil {
			score = *p.Score
			scoreSeen = true
		}
		ps := &PeerScore{
			PeerID:          p.PeerID,
			State:           strings.ToLower(p.State),
			Direction:       strings.ToLower(p.Direction),
			Score:           score,
			ScoreNormalized: normalizeAroundNeutral(score, min, max, neutral),
			ScoreMin:        min,
			ScoreMax:        max,
			AgentVersion:    agentVersion,
			FetchedAt:       fetched,
		}
		ps.ScoreState = scoreStateFor(ps.ScoreNormalized)
		if len(p.DownscoreReasons) > 0 {
			ps.LastEvent = &PeerScoreEvent{
				Reason:       p.DownscoreReasons[0],
				NativeReason: p.DownscoreReasons[0],
			}
		}
		if p.DisconnectReason != "" {
			ps.LastDisconnect = &PeerScoreEvent{
				Reason:       p.DisconnectReason,
				NativeReason: p.DisconnectReason,
			}
		}
		synthesizeLastEvent(ps)
		out = append(out, ps)
	}
	if !scoreSeen && len(out) > 0 {
		return nil, ErrNotSupported
	}
	return out, nil
}

// --- Teku -------------------------------------------------------------------

type tekuPeerScoresResponse struct {
	Data []struct {
		PeerID          string  `json:"peer_id"`
		GossipScore     float64 `json:"gossip_score"`
		ReputationScore *int    `json:"reputation_score,omitempty"`
		LastAction      *struct {
			Reason     string  `json:"reason"`
			Delta      float64 `json:"delta"`
			SecondsAgo uint64  `json:"seconds_ago"`
		} `json:"last_action,omitempty"`
	} `json:"data"`
}

// GetTekuPeerScores polls Teku's /teku/v1/nodes/peer_scores endpoint.
// The patched Teku exposes reputation_score (range [-10, +20] from
// ReputationAdjustment) and last_action {reason, delta, seconds_ago}.
// Vanilla Teku still returns only peer_id + gossip_score; those fields
// are optional so this fetcher handles both shapes.
func (bc *BeaconClient) GetTekuPeerScores(ctx context.Context) ([]*PeerScore, error) {
	var resp tekuPeerScoresResponse
	url := fmt.Sprintf("%s/teku/v1/nodes/peer_scores", bc.endpoint)
	if err := bc.getJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("teku peer scores: %w", err)
	}

	// Teku's ReputationAdjustment range is asymmetric [-10, +20] with
	// neutral=0 (default starting reputation, not the range midpoint).
	// Reputation is the single value Teku compares against its ban
	// threshold, so it is always the canonical Score — missing entries
	// from ReputationManager are reported as 0 (Teku's own default)
	// rather than falling back to gossipsub. That keeps the Score
	// semantics consistent across all of Teku's peers.
	const tkRepMin, tkRepMax, tkRepNeutral = -10.0, 20.0, 0.0
	out := make([]*PeerScore, 0, len(resp.Data))
	fetched := nowMs()
	for _, item := range resp.Data {
		gossip := item.GossipScore
		var rep float64
		if item.ReputationScore != nil {
			rep = float64(*item.ReputationScore)
		}
		ps := &PeerScore{
			PeerID:          item.PeerID,
			Score:           rep,
			ScoreNormalized: normalizeAroundNeutral(rep, tkRepMin, tkRepMax, tkRepNeutral),
			ScoreMin:        tkRepMin,
			ScoreMax:        tkRepMax,
			FetchedAt:       fetched,
		}
		ps.ScoreState = scoreStateFor(ps.ScoreNormalized)
		ps.Components.Gossipsub = &gossip
		ps.Components.Reputation = &rep
		if a := item.LastAction; a != nil {
			ps.LastEvent = &PeerScoreEvent{
				Reason:       translateTekuReason(a.Reason),
				NativeReason: a.Reason,
				Delta:        a.Delta,
				SecondsAgo:   a.SecondsAgo,
			}
		}
		synthesizeLastEvent(ps)
		out = append(out, ps)
	}
	return out, nil
}

// --- Prysm ------------------------------------------------------------------

type prysmPeerScoresResponse struct {
	GeneratedAt int64 `json:"generated_at"`
	Peers       []struct {
		PeerID                  string  `json:"peer_id"`
		PeerIDShort             string  `json:"peer_id_short"`
		Implementation          string  `json:"implementation"`
		ConnectionState         string  `json:"connection_state"`
		StartScore              float64 `json:"start_score"`
		CurrentScore            float64 `json:"current_score"`
		BehaviourPenalty        float64 `json:"behaviour_penalty"`
		RatePerMin              float64 `json:"rate_per_min"`
		LastDelta               float64 `json:"last_delta"`
		LastDownscoreTopic      string  `json:"last_downscore_topic"`
		LastDownscoreInfo       string  `json:"last_downscore_info"`
		LastDownscoreSecondsAgo int64   `json:"last_downscore_seconds_ago"`
		GossipScore             float64 `json:"gossip_score"`
		PeerStatusScore         float64 `json:"peer_status_score"`
		BadResponseScore        float64 `json:"bad_response_score"`
		BadResponses            float64 `json:"bad_responses"`
	} `json:"peers"`
}

// GetPrysmPeerScores polls Prysm's /prysm/v1/node/peer_scores endpoint.
// Prysm reports an asymmetric range (min=-100, max=+1), so the
// normalization uses normalizePrysm.
func (bc *BeaconClient) GetPrysmPeerScores(ctx context.Context) ([]*PeerScore, error) {
	var resp prysmPeerScoresResponse
	url := fmt.Sprintf("%s/prysm/v1/node/peer_scores", bc.endpoint)
	if err := bc.getJSON(ctx, url, &resp); err != nil {
		return nil, fmt.Errorf("prysm peer scores: %w", err)
	}

	const prMin, prMax = -100.0, 1.0
	out := make([]*PeerScore, 0, len(resp.Peers))
	fetched := nowMs()
	for _, item := range resp.Peers {
		score := item.CurrentScore
		gossip := item.GossipScore
		status := item.PeerStatusScore
		badResp := item.BadResponseScore
		badCount := item.BadResponses
		penalty := item.BehaviourPenalty
		ps := &PeerScore{
			PeerID:          item.PeerID,
			State:           strings.ToLower(item.ConnectionState),
			Score:           score,
			ScoreNormalized: normalizePrysm(score),
			ScoreMin:        prMin,
			ScoreMax:        prMax,
			AgentVersion:    item.Implementation,
			FetchedAt:       fetched,
		}
		ps.ScoreState = scoreStateFor(ps.ScoreNormalized)
		ps.Components.Gossipsub = &gossip
		ps.Components.PeerStatus = &status
		ps.Components.BadResponses = &badResp
		ps.Components.BehaviourPenalty = &penalty
		_ = badCount

		if (item.LastDownscoreInfo != "" || item.LastDownscoreTopic != "") && item.LastDownscoreSecondsAgo >= 0 {
			native := item.LastDownscoreInfo
			if native == "" {
				native = item.LastDownscoreTopic
			}
			ps.LastEvent = &PeerScoreEvent{
				Reason:       translatePrysmReason(item.LastDownscoreTopic, item.LastDownscoreInfo),
				NativeReason: native,
				Delta:        item.LastDelta,
				Topic:        item.LastDownscoreTopic,
				SecondsAgo:   uint64(item.LastDownscoreSecondsAgo),
			}
		}
		synthesizeLastEvent(ps)
		out = append(out, ps)
	}
	return out, nil
}
