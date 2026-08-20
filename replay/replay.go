package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

const (
	// headSearchDepth bounds how far back the replay looks for the newest block at or
	// before the start slot before giving up.
	headSearchDepth = 256

	// finalityFallbackEpochs bounds how far back the replay looks for a state it can
	// read finality from when the head state itself has been pruned upstream.
	finalityFallbackEpochs = 8

	// upstreamPollInterval is how often the head of the real chain is re-read, purely
	// so the UI can show how far the replay still has to go.
	upstreamPollInterval = 30 * time.Second

	// stateLoadLeadSlots is how many slots the replay may still serve while the explorer
	// is loading a beacon state. It is the slack that keeps blocks flowing during a
	// multi-second read, so the explorer's block indexer is not idled by a wait that
	// only its state loader is in.
	stateLoadLeadSlots = 4
)

// Replay steps a past slot range through a fake beacon/execution node pair, driving a
// virtual clock so an explorer pointed at it sees the range unfold as if it were live.
type Replay struct {
	logger   logrus.FieldLogger
	cfg      Config
	chain    *chainInfo
	upstream *upstream
	events   *eventHub
	control  *eventHub
	clock    *clock
	el       *elProxy
	states   *stateLoads

	// wake nudges the driver whenever the drive state changed.
	wake chan struct{}

	// stateLeadFrom is the slot at which the explorer last started loading a state, or
	// 0 when it is not loading one. Driver-owned: only advanceSlot touches it.
	stateLeadFrom uint64

	mutex         sync.RWMutex
	virtualSlot   uint64
	head          *blockHeader
	finality      *finalityCheckpoints
	running       bool
	speed         float64
	target        uint64
	upstreamSlot  uint64
	upstreamAtUTC time.Time

	servers []*http.Server
}

// New prepares a replay: it reads genesis and the timing specs from upstream, resolves
// the head at the start slot and positions the virtual clock there.
func New(ctx context.Context, logger logrus.FieldLogger, cfg Config) (*Replay, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	up, err := newUpstream(logger.WithField("module", "upstream"), &cfg)
	if err != nil {
		return nil, err
	}

	chain, err := loadChainInfo(ctx, up)
	if err != nil {
		return nil, err
	}

	up.chain = chain

	logger.WithFields(logrus.Fields{
		"genesis":       chain.genesisTime.Format(time.RFC3339),
		"slotDuration":  chain.slotDuration,
		"slotsPerEpoch": chain.slotsPerEpoch,
	}).Info("loaded chain info from upstream")

	replay := &Replay{
		logger:      logger,
		cfg:         cfg,
		chain:       chain,
		upstream:    up,
		events:      newEventHub(logger.WithField("module", "events")),
		control:     newLossyEventHub(logger.WithField("module", "control")),
		states:      newStateLoads(),
		clock:       newClock(chain.slotTime(cfg.StartSlot).Add(chain.payloadOffset())),
		wake:        make(chan struct{}, 1),
		virtualSlot: cfg.StartSlot,
	}

	if cfg.ExecutionURL != "" {
		replay.el = newELProxy(logger.WithField("module", "el"), cfg.ExecutionURL)
	}

	if err := replay.initHead(ctx); err != nil {
		return nil, err
	}

	if replay.el != nil {
		if err := replay.el.init(ctx, chain.slotTime(cfg.StartSlot)); err != nil {
			return nil, fmt.Errorf("error initializing execution head: %w", err)
		}
	}

	return replay, nil
}

// blockOffset is how far into a slot the block and head events are replayed, and
// payloadOffset when the payload becomes available. Both approximate real node timing.
func (c *chainInfo) blockOffset() time.Duration {
	return c.slotDuration / 3
}

func (c *chainInfo) payloadOffset() time.Duration {
	return c.slotDuration * 2 / 3
}

// stateGateOffset is how far into a slot the replay checks whether the explorer is
// still loading a beacon state.
func (c *chainInfo) stateGateOffset() time.Duration {
	return c.slotDuration / 2
}

// initHead walks back from the start slot to the newest block, which becomes the head
// the fake node reports before the first step.
func (r *Replay) initHead(ctx context.Context) error {
	slot := r.cfg.StartSlot

	for depth := 0; depth < headSearchDepth; depth++ {
		header, err := r.upstream.headerBySlot(ctx, slot)
		if err != nil {
			return fmt.Errorf("error resolving head at slot %v: %w", slot, err)
		}

		if header != nil {
			r.mutex.Lock()
			r.head = header
			r.mutex.Unlock()

			if err := r.refreshFinality(ctx, header); err != nil {
				return err
			}

			r.logger.WithFields(logrus.Fields{
				"slot":      header.Slot,
				"root":      header.Root,
				"finalized": r.finality.FinalizedEpoch,
			}).Info("resolved replay head")

			return nil
		}

		if slot == 0 {
			break
		}

		slot--
	}

	return fmt.Errorf("no block found within %v slots before slot %v", headSearchDepth, r.cfg.StartSlot)
}

// Start brings up the fake nodes and the control server and starts the driver.
func (r *Replay) Start(ctx context.Context) error {
	if err := r.listen(ctx, r.cfg.CLListen, r.clHandler(), "consensus"); err != nil {
		return err
	}

	if r.el != nil {
		if err := r.listen(ctx, r.cfg.ELListen, r.el, "execution"); err != nil {
			return err
		}
	}

	if err := r.listen(ctx, r.cfg.ControlListen, r.controlHandler(), "control"); err != nil {
		return err
	}

	go r.runDriver(ctx)
	go r.runUpstreamPoller(ctx)

	if r.cfg.Speed > 0 {
		r.Play(r.cfg.Speed)
	}

	return nil
}

func (r *Replay) listen(ctx context.Context, addr string, handler http.Handler, name string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("could not listen on %v for the %v endpoint: %w", addr, name, err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}

	r.servers = append(r.servers, server)

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			r.logger.WithError(err).Errorf("%v endpoint stopped", name)
		}
	}()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			r.logger.WithError(err).Debugf("error shutting down %v endpoint", name)
		}
	}()

	r.logger.Infof("%v endpoint listening on %v", name, addr)

	return nil
}

// Stop shuts the fake nodes down.
func (r *Replay) Stop() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, server := range r.servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}

	return nil
}

// -- drive state ------------------------------------------------------------------

// Play runs the replay continuously at the given multiple of real time.
func (r *Replay) Play(speed float64) {
	r.mutex.Lock()
	r.running = true
	r.speed = speed
	r.target = 0
	r.mutex.Unlock()

	r.clock.setRate(speed)
	r.nudge()
	r.notifyStatus()
}

// Step advances a fixed number of slots as fast as upstream allows, then pauses.
func (r *Replay) Step(slots uint64) {
	if slots == 0 {
		slots = 1
	}

	r.mutex.Lock()
	r.running = true
	r.speed = 0
	r.target = r.virtualSlot + slots
	r.mutex.Unlock()

	r.clock.setRate(0)
	r.nudge()
	r.notifyStatus()
}

// Forward runs to a slot, emitting every slot on the way, then pauses. A speed of 0
// advances as fast as upstream allows; anything else plays at that multiple of real
// time, which gives the explorer a predictable amount of time per slot to keep up.
func (r *Replay) Forward(slot uint64, speed float64) error {
	r.mutex.Lock()
	if slot <= r.virtualSlot {
		current := r.virtualSlot
		r.mutex.Unlock()

		return fmt.Errorf("slot %v is not ahead of the current head slot %v (the replay cannot rewind)", slot, current)
	}

	r.running = true
	r.speed = speed
	r.target = slot
	r.mutex.Unlock()

	r.clock.setRate(speed)
	r.nudge()
	r.notifyStatus()

	return nil
}

// Pause freezes the replay and the virtual clock where they are.
func (r *Replay) Pause() {
	r.mutex.Lock()
	r.running = false
	r.mutex.Unlock()

	r.clock.setRate(0)
	r.nudge()
	r.notifyStatus()
}

// Resume continues what the replay was doing before it was paused, keeping both the
// speed and any target it was running towards. A target that has already been reached
// is dropped, so resuming after a completed step runs on rather than pausing again.
func (r *Replay) Resume() {
	r.mutex.Lock()
	r.running = true
	if r.target != 0 && r.target <= r.virtualSlot {
		r.target = 0
	}
	speed := r.speed
	r.mutex.Unlock()

	r.clock.setRate(speed)
	r.nudge()
	r.notifyStatus()
}

// SetSpeed changes the playback rate without starting or stopping the replay.
func (r *Replay) SetSpeed(speed float64) {
	if speed < 0 {
		speed = 0
	}

	r.mutex.Lock()
	r.speed = speed
	running := r.running
	r.mutex.Unlock()

	if running {
		r.clock.setRate(speed)
	}

	r.nudge()
	r.notifyStatus()
}

func (r *Replay) nudge() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Replay) isRunning() bool {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	return r.running
}

// Status is a snapshot of the replay for the console, the control endpoint and the
// explorer's own replay UI.
type Status struct {
	VirtualSlot  uint64    `json:"virtual_slot"`
	VirtualEpoch uint64    `json:"virtual_epoch"`
	VirtualTime  time.Time `json:"virtual_time"`

	HeadSlot       uint64 `json:"head_slot"`
	HeadRoot       string `json:"head_root"`
	FinalizedEpoch uint64 `json:"finalized_epoch"`
	JustifiedEpoch uint64 `json:"justified_epoch"`
	ExecutionBlock uint64 `json:"execution_block"`

	Running    bool    `json:"running"`
	Speed      float64 `json:"speed"`
	Rate       float64 `json:"rate"`
	StartSlot  uint64  `json:"start_slot"`
	TargetSlot uint64  `json:"target_slot"`

	// StateLoads is how many beacon states the explorer is pulling right now, and
	// Holding says the replay's clock is frozen waiting for them.
	StateLoads int  `json:"state_loads"`
	Holding    bool `json:"holding"`

	// UpstreamSlot is the head of the real chain, so the UI can show how much of it is
	// still ahead of the replay.
	UpstreamSlot  uint64    `json:"upstream_slot"`
	UpstreamEpoch uint64    `json:"upstream_epoch"`
	UpstreamSeen  time.Time `json:"upstream_seen"`

	GenesisTime    time.Time `json:"genesis_time"`
	SlotDurationMs uint64    `json:"slot_duration_ms"`
	SlotsPerEpoch  uint64    `json:"slots_per_epoch"`

	Subscribers int    `json:"event_subscribers"`
	Upstream    string `json:"upstream"`
	Tracoor     bool   `json:"tracoor"`
}

// Status returns a snapshot of where the replay currently stands.
func (r *Replay) Status() Status {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	virtualTime, rate := r.clock.state()

	status := Status{
		VirtualSlot:    r.virtualSlot,
		VirtualEpoch:   r.chain.epochOf(r.virtualSlot),
		VirtualTime:    virtualTime.UTC(),
		Running:        r.running,
		Speed:          r.speed,
		Rate:           rate,
		StartSlot:      r.cfg.StartSlot,
		TargetSlot:     r.target,
		UpstreamSlot:   r.upstreamSlot,
		UpstreamEpoch:  r.chain.epochOf(r.upstreamSlot),
		UpstreamSeen:   r.upstreamAtUTC,
		GenesisTime:    r.chain.genesisTime,
		SlotDurationMs: uint64(r.chain.slotDuration / time.Millisecond),
		SlotsPerEpoch:  r.chain.slotsPerEpoch,
		StateLoads:     r.states.count(),
		Holding:        r.clock.isHeld(),
		Subscribers:    r.events.subscriberCount(),
		Upstream:       redactURL(r.cfg.UpstreamURL),
		Tracoor:        r.upstream.tracoor != nil,
	}

	if r.head != nil {
		status.HeadSlot = r.head.Slot
		status.HeadRoot = r.head.Root
	}

	if r.finality != nil {
		status.FinalizedEpoch = r.finality.FinalizedEpoch
		status.JustifiedEpoch = r.finality.JustifiedEpoch
	}

	if r.el != nil {
		status.ExecutionBlock = r.el.head()
	}

	return status
}

// -- driver -----------------------------------------------------------------------

func (r *Replay) runDriver(ctx context.Context) {
	for {
		if !r.isRunning() {
			if err := r.awaitResume(ctx); err != nil {
				return
			}

			continue
		}

		r.mutex.RLock()
		next := r.virtualSlot + 1
		target := r.target
		r.mutex.RUnlock()

		if target != 0 && next > target {
			r.logger.Infof("reached slot %v, pausing", target)
			r.Pause()

			continue
		}

		if err := r.advanceSlot(ctx, next); err != nil {
			if ctx.Err() != nil {
				return
			}

			r.logger.WithError(err).Errorf("failed advancing to slot %v, pausing", next)
			r.Pause()
		}
	}
}

// runUpstreamPoller keeps track of where the real chain currently is, which is what the
// replay is progressing through. It is only informational, so a failed read is ignored.
func (r *Replay) runUpstreamPoller(ctx context.Context) {
	ticker := time.NewTicker(upstreamPollInterval)
	defer ticker.Stop()

	for {
		if header, err := r.upstream.headerByRoot(ctx, "head"); err == nil && header != nil {
			r.mutex.Lock()
			changed := r.upstreamSlot != header.Slot
			r.upstreamSlot = header.Slot
			r.upstreamAtUTC = time.Now().UTC()
			r.mutex.Unlock()

			if changed {
				r.notifyStatus()
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Replay) awaitResume(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.wake:
			if r.isRunning() {
				return nil
			}
		}
	}
}

// advanceSlot moves the replay to the given slot in three phases, mirroring how a real
// node experiences a slot: the boundary, the block, and the payload becoming available.
func (r *Replay) advanceSlot(ctx context.Context, slot uint64) error {
	boundary := r.chain.slotTime(slot)

	if err := r.waitForVirtual(ctx, boundary); err != nil {
		return err
	}

	r.mutex.Lock()
	r.virtualSlot = slot
	r.mutex.Unlock()

	header, err := r.upstream.headerBySlot(ctx, slot)
	if err != nil {
		return fmt.Errorf("error fetching header: %w", err)
	}

	if err := r.waitForVirtual(ctx, boundary.Add(r.chain.blockOffset())); err != nil {
		return err
	}

	if header != nil {
		if err := r.emitBlock(ctx, header); err != nil {
			return err
		}
	}

	// halfway through the slot, wait for any beacon state the explorer is loading. The
	// clock is frozen while waiting, so a slow state read costs real time but no
	// virtual time: without this the replay would run on and the explorer would be
	// several slots behind the moment the state finally arrived.
	if err := r.waitForVirtual(ctx, boundary.Add(r.chain.stateGateOffset())); err != nil {
		return err
	}

	if err := r.awaitStateLoads(ctx, slot); err != nil {
		return err
	}

	if err := r.waitForVirtual(ctx, boundary.Add(r.chain.payloadOffset())); err != nil {
		return err
	}

	if err := r.advanceExecution(ctx, slot, boundary, header); err != nil {
		return err
	}

	r.notifyStatus()

	if !r.isPlaying() {
		if err := r.settle(ctx); err != nil {
			return err
		}
	}

	return nil
}

// awaitStateLoads keeps the replay from running away while the explorer is loading a
// beacon state, without stalling it outright.
//
// The explorer loads states on a different goroutine than it indexes blocks on, so a
// state read of several seconds does not stop it from processing blocks — it only stops
// it from finishing that epoch's stats. Holding the whole replay for the read therefore
// idles the block indexer for nothing, and emits no block or head events at all while it
// lasts. So the replay is allowed to run stateLoadLeadSlots further while a read is in
// flight, and only holds once it would get further ahead than that.
//
// Once it does hold, the clock is frozen, so the wait costs real time and no virtual
// time. It gives up after StateHoldTimeout so a stuck read cannot wedge the replay.
func (r *Replay) awaitStateLoads(ctx context.Context, slot uint64) error {
	idle := r.states.idleChan()

	select {
	case <-idle:
		r.stateLeadFrom = 0

		return nil
	default:
	}

	// first slot of this busy period: remember where the explorer started falling behind
	if r.stateLeadFrom == 0 {
		r.stateLeadFrom = slot
	}

	if slot-r.stateLeadFrom < stateLoadLeadSlots {
		// let the replay run on; the explorer can index these blocks meanwhile
		return nil
	}

	r.clock.hold()
	r.notifyStatus()

	defer func() {
		r.clock.release()
		r.stateLeadFrom = 0
		r.notifyStatus()
	}()

	started := time.Now()
	pending := r.states.count()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-idle:
		r.logger.Debugf("held %v for %v state load(s)", time.Since(started).Round(time.Millisecond), pending)

	case <-time.After(r.cfg.StateHoldTimeout):
		r.logger.Warnf("%v state load(s) did not finish within %v, continuing", pending, r.cfg.StateHoldTimeout)
	}

	return nil
}

func (r *Replay) isPlaying() bool {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	return r.running && r.speed > 0
}

func (r *Replay) settle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(r.cfg.StepSettle):
		return nil
	}
}

// waitForVirtual blocks until the virtual clock reaches a point in time. While playing
// that means sleeping the corresponding amount of real time; while stepping the clock
// simply jumps there, and while paused it waits for the replay to be resumed.
func (r *Replay) waitForVirtual(ctx context.Context, target time.Time) error {
	for {
		if !r.isRunning() {
			if err := r.awaitResume(ctx); err != nil {
				return err
			}

			continue
		}

		if !r.isPlaying() {
			if r.clock.now().Before(target) {
				r.clock.set(target)
			}

			return nil
		}

		delay := r.clock.realDelayUntil(target)
		if delay <= 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		case <-r.wake:
		}
	}
}

// emitBlock publishes the events a real node emits for a new block, after making the
// block the head so that anything the explorer requests in response already resolves.
func (r *Replay) emitBlock(ctx context.Context, header *blockHeader) error {
	if r.cfg.EmitBids && r.chain.bidsActiveAt(header.Slot) {
		bid, err := r.upstream.payloadBid(ctx, header.Root)
		if err != nil {
			r.logger.WithError(err).Debugf("could not read payload bid of block %v", header.Root)
		} else if bid != nil {
			r.events.publish(sseEvent{topic: "execution_payload_bid", data: bid})
		}
	}

	r.mutex.Lock()
	previousEpoch := uint64(0)
	if r.head != nil {
		previousEpoch = r.chain.epochOf(r.head.Slot)
	}
	r.head = header
	r.mutex.Unlock()

	blockEvent := &v1.BlockEvent{
		Slot:  phase0.Slot(header.Slot),
		Block: parseRoot(header.Root),
	}
	r.publishJSON("block", blockEvent)

	headEvent := &v1.HeadEvent{
		Slot:            phase0.Slot(header.Slot),
		Block:           parseRoot(header.Root),
		State:           parseRoot(header.StateRoot),
		EpochTransition: header.Slot%r.chain.slotsPerEpoch == 0,
	}
	r.publishJSON("head", headEvent)

	// finality only ever moves at an epoch boundary, so it is re-read once per epoch
	// rather than for every block
	if r.chain.epochOf(header.Slot) != previousEpoch {
		if err := r.refreshFinality(ctx, header); err != nil {
			r.logger.WithError(err).Warnf("could not refresh finality at slot %v", header.Slot)
		}
	}

	return nil
}

// refreshFinality reads the finality the chain knew at a head block and emits a
// finalized_checkpoint event when it moved.
func (r *Replay) refreshFinality(ctx context.Context, header *blockHeader) error {
	finality, err := r.readFinality(ctx, header)
	if err != nil {
		return err
	}

	r.mutex.Lock()
	previous := r.finality
	r.finality = finality
	r.mutex.Unlock()

	// the initial read only establishes the baseline, it is not a new checkpoint
	if previous == nil || previous.FinalizedEpoch == finality.FinalizedEpoch {
		return nil
	}

	finalizedHeader, err := r.upstream.headerByRoot(ctx, finality.FinalizedRoot)
	if err != nil || finalizedHeader == nil {
		r.logger.Debugf("could not resolve finalized header %v", finality.FinalizedRoot)
		return nil
	}

	event := &v1.FinalizedCheckpointEvent{
		Block: parseRoot(finality.FinalizedRoot),
		State: parseRoot(finalizedHeader.StateRoot),
		Epoch: phase0.Epoch(finality.FinalizedEpoch),
	}
	r.publishJSON("finalized_checkpoint", event)

	r.logger.Infof("finalized checkpoint moved to epoch %v", finality.FinalizedEpoch)

	return nil
}

// readFinality asks the upstream what the chain had finalized at a head block. Reading
// finality forces the node to load that state, and nodes keep only a shallow window of
// them, so a pruned head state falls back to the epoch boundaries below it: finality
// only moves at an epoch boundary, so the epoch's first block answers for the whole
// epoch.
func (r *Replay) readFinality(ctx context.Context, header *blockHeader) (*finalityCheckpoints, error) {
	finality, err := r.upstream.finality(ctx, header.StateRoot)
	if err == nil {
		return finality, nil
	}

	if err != errNotFound {
		return nil, err
	}

	epoch := r.chain.epochOf(header.Slot)

	for attempt := 0; attempt < finalityFallbackEpochs && epoch > 0; attempt++ {
		boundary, headerErr := r.upstream.headerBySlot(ctx, epoch*r.chain.slotsPerEpoch)
		epoch--

		if headerErr != nil || boundary == nil {
			continue
		}

		finality, err = r.upstream.finality(ctx, boundary.StateRoot)
		if err == nil {
			r.logger.Debugf("read finality from epoch boundary slot %v (head state was pruned)", boundary.Slot)

			return finality, nil
		}

		if err != errNotFound {
			return nil, err
		}
	}

	return nil, fmt.Errorf("upstream has no state to read finality from at slot %v; use an archive node or a node that keeps historical states", header.Slot)
}

// advanceExecution moves the virtual execution head to the last block produced at or
// before this slot. A new execution block whose timestamp is exactly this slot's time
// means the payload for this slot was revealed, which is what the availability event
// reports post-Gloas.
func (r *Replay) advanceExecution(ctx context.Context, slot uint64, boundary time.Time, header *blockHeader) error {
	if r.el == nil {
		return nil
	}

	added, err := r.el.advanceTo(ctx, boundary)
	if err != nil {
		return fmt.Errorf("error advancing execution head: %w", err)
	}

	if header == nil {
		return nil
	}

	for _, block := range added {
		if block.timestamp.Equal(boundary) {
			event := &v1.ExecutionPayloadAvailableEvent{
				BlockRoot: parseRoot(header.Root),
				Slot:      phase0.Slot(slot),
			}
			r.publishJSON("execution_payload_available", event)

			break
		}
	}

	return nil
}

// redactURL strips credentials from an endpoint so they do not end up on the console
// or in the status endpoint.
func redactURL(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}

	return parsed.Redacted()
}

func (r *Replay) publishJSON(topic string, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		r.logger.WithError(err).Errorf("could not encode %v event", topic)
		return
	}

	r.events.publish(sseEvent{topic: topic, data: data})
}
