package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/consensus/rpc"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

// peerScoresPersisterBufferSize bounds the persister's in-memory
// snapshot backlog. Each entry is one reporter's full peer set; on a
// 12s poll cadence with the default ~50 peers per CL this comfortably
// outpaces the writer even on cold storage.
const peerScoresPersisterBufferSize = 64

// peerScoresSnapshot is the unit of work handed to the persister
// goroutine: one reporter's full set of peer scores at a moment in
// time.
type peerScoresSnapshot struct {
	client *consensus.Client
	scores []*rpc.PeerScore
	when   time.Time
}

// PeerScoresPersister consumes per-reporter peer-score snapshots
// emitted by consensus.Client poll loops and persists the samples /
// events into the dora database. It is a singleton; one instance is
// wired into consensus.Pool when feature + database are both enabled.
type PeerScoresPersister struct {
	ctx    context.Context
	logger logrus.FieldLogger
	queue  chan *peerScoresSnapshot
	wg     sync.WaitGroup

	// lastEventKey tracks the most recent event we have already
	// recorded for a (reporter, target) pair, keyed by reporter pid
	// + "|" + target pid. The value is "observedAtMs|reasonCode" and
	// is used to dedupe rapid-fire repeats of the same event across
	// poll ticks.
	eventDedupMu sync.Mutex
	lastEventKey map[string]string
}

var globalPeerScoresPersister *PeerScoresPersister

// StartPeerScoresPersister spins up the singleton persister and wires
// it into the given consensus pool. Idempotent: subsequent calls are
// no-ops. Returns nil when the feature is disabled.
func StartPeerScoresPersister(ctx context.Context, logger logrus.FieldLogger, pool *consensus.Pool) *PeerScoresPersister {
	if globalPeerScoresPersister != nil {
		return globalPeerScoresPersister
	}
	if utils.Config.PeerScores == nil || !utils.Config.PeerScores.Enabled {
		return nil
	}
	if db.ReaderDb == nil {
		logger.Warn("peer-scores persister not started: database not initialised")
		return nil
	}

	p := &PeerScoresPersister{
		ctx:          ctx,
		logger:       logger.WithField("service", "peer-scores-persister"),
		queue:        make(chan *peerScoresSnapshot, peerScoresPersisterBufferSize),
		lastEventKey: make(map[string]string, 256),
	}
	globalPeerScoresPersister = p

	pool.SetPeerScoresPersister(p.enqueue)

	p.wg.Add(1)
	go p.run()

	p.logger.Info("peer-scores persister started")
	return p
}

// enqueue is the consensus.Pool callback; non-blocking. Snapshots are
// dropped on the floor if the queue is saturated (which only happens
// if the DB writer is wedged - in that case the next poll tick will
// re-supply current state).
func (p *PeerScoresPersister) enqueue(client *consensus.Client, scores []*rpc.PeerScore) {
	if client == nil || len(scores) == 0 {
		return
	}

	snap := &peerScoresSnapshot{
		client: client,
		scores: scores,
		when:   time.Now(),
	}

	select {
	case p.queue <- snap:
	default:
		p.logger.Warn("peer-scores persister queue full, dropping snapshot")
	}
}

// run is the persister goroutine; drains snapshots until ctx is
// cancelled.
func (p *PeerScoresPersister) run() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		case snap := <-p.queue:
			if err := p.writeSnapshot(snap); err != nil {
				p.logger.WithError(err).Debug("failed to persist peer-scores snapshot")
			}
		}
	}
}

// writeSnapshot turns one reporter snapshot into samples + (deduped)
// events and inserts both in a single tx.
func (p *PeerScoresPersister) writeSnapshot(snap *peerScoresSnapshot) error {
	reporterID := ""
	if id := snap.client.GetNodeIdentity(); id != nil {
		reporterID = id.PeerID
	}
	if reporterID == "" {
		return fmt.Errorf("reporter has no node identity yet")
	}

	reporterType := int16(snap.client.GetClientType())
	observedAt := snap.when.UnixMilli()

	samples := make([]*dbtypes.ClPeerScoreSample, 0, len(snap.scores))
	events := make([]*dbtypes.ClPeerScoreEvent, 0, len(snap.scores))

	for _, score := range snap.scores {
		if score == nil || score.PeerID == "" {
			continue
		}

		var componentsJSON *string
		if hasAnyComponent(score.Components) {
			if b, err := json.Marshal(score.Components); err == nil {
				s := string(b)
				componentsJSON = &s
			}
		}

		var lastEventJSON *string
		if score.LastEvent != nil {
			if b, err := json.Marshal(score.LastEvent); err == nil {
				s := string(b)
				lastEventJSON = &s
			}
		}

		samples = append(samples, &dbtypes.ClPeerScoreSample{
			ObservedAt:         observedAt,
			ReporterPeerID:     reporterID,
			ReporterClientType: reporterType,
			TargetPeerID:       score.PeerID,
			Score:              score.Score,
			ScoreNormalized:    score.ScoreNormalized,
			ScoreState:         score.ScoreState,
			ComponentsJSON:     componentsJSON,
			LastEventJSON:      lastEventJSON,
		})

		if score.LastEvent == nil || score.LastEvent.Reason == "" {
			continue
		}

		eventObservedAt := observedAt - int64(score.LastEvent.SecondsAgo)*1000
		if eventObservedAt < 0 {
			eventObservedAt = observedAt
		}

		dedupKey := reporterID + "|" + score.PeerID
		dedupVal := fmt.Sprintf("%d|%s", eventObservedAt, score.LastEvent.Reason)

		p.eventDedupMu.Lock()
		prev := p.lastEventKey[dedupKey]
		if prev == dedupVal {
			p.eventDedupMu.Unlock()
			continue
		}
		p.lastEventKey[dedupKey] = dedupVal
		p.eventDedupMu.Unlock()

		var delta *float64
		if score.LastEvent.Delta != 0 {
			d := score.LastEvent.Delta
			delta = &d
		}
		var topic *string
		if score.LastEvent.Topic != "" {
			t := score.LastEvent.Topic
			topic = &t
		}
		var direction *string
		if score.LastEvent.Direction != "" {
			d := score.LastEvent.Direction
			direction = &d
		}

		events = append(events, &dbtypes.ClPeerScoreEvent{
			ObservedAt:     eventObservedAt,
			ReporterPeerID: reporterID,
			TargetPeerID:   score.PeerID,
			ReasonCode:     score.LastEvent.Reason,
			NativeReason:   score.LastEvent.NativeReason,
			Category:       rpc.CategoryFor(score.LastEvent.Reason),
			Delta:          delta,
			Topic:          topic,
			Direction:      direction,
		})
	}

	if len(samples) == 0 && len(events) == 0 {
		return nil
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		if err := db.InsertPeerScoreSamples(p.ctx, tx, samples); err != nil {
			return err
		}
		return db.InsertPeerScoreEvents(p.ctx, tx, events)
	})
	if err != nil {
		return fmt.Errorf("peer-scores tx: %w", err)
	}

	p.logger.WithFields(logrus.Fields{
		"reporter": reporterID[:min(12, len(reporterID))],
		"samples":  len(samples),
		"events":   len(events),
	}).Debug("persisted peer-scores snapshot")
	return nil
}

// Stop drains the persister cleanly.
func (p *PeerScoresPersister) Stop() {
	p.wg.Wait()
}

func hasAnyComponent(c rpc.PeerScoreComponents) bool {
	return c.Gossipsub != nil ||
		c.Reputation != nil ||
		c.BadResponses != nil ||
		c.PeerStatus != nil ||
		c.BlockProvider != nil ||
		c.BehaviourPenalty != nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
