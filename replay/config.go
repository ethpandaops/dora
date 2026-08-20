package replay

import (
	"fmt"
	"time"
)

// Config describes a replay run: where the real chain data comes from, where the
// fake nodes listen, and where in the chain the replay starts.
type Config struct {
	// UpstreamURL is the beacon node the consensus proxy reads from. It must serve
	// historical blocks and states for the replayed range, so point it at a single
	// known-archive node rather than at a load balancer over mixed-retention nodes.
	UpstreamURL string

	// ExecutionURL is the execution node the JSON-RPC proxy reads from. When empty
	// the execution proxy is not started.
	ExecutionURL string

	// TracoorURL and TracoorNetwork enable the tracoor fallback for artifacts the
	// beacon node has pruned. Tracoor indexes by root, so it can only serve blocks
	// and states whose root is already known from a header.
	TracoorURL     string
	TracoorNetwork string

	CLListen      string
	ELListen      string
	ControlListen string

	// StartSlot is the first slot the replay serves. The head is initialized to the
	// newest block at or before this slot.
	StartSlot uint64

	// Speed is the initial playback rate in virtual seconds per real second. A value
	// of 0 starts the replay paused.
	Speed float64

	// CacheDir, when set, stores every immutable artifact fetched from upstream so a
	// range is downloaded once and replays offline afterwards.
	CacheDir string

	// EmitBids controls whether the winning execution payload bid of each Gloas block
	// is replayed on the event stream. It costs one extra block fetch per slot.
	EmitBids bool

	// StepSettle is the real time the driver pauses at each phase of a stepped slot,
	// giving the explorer a chance to observe the slot boundary and process events.
	StepSettle time.Duration

	// StateHoldTimeout bounds how long the replay freezes its clock waiting for the
	// explorer to finish loading a beacon state, so a stuck read cannot wedge a run.
	StateHoldTimeout time.Duration
}

// DefaultConfig returns a config with the non-chain-specific defaults filled in.
func DefaultConfig() Config {
	return Config{
		CLListen:         "127.0.0.1:15052",
		ELListen:         "127.0.0.1:15545",
		ControlListen:    "127.0.0.1:15000",
		Speed:            0,
		EmitBids:         true,
		StepSettle:       250 * time.Millisecond,
		StateHoldTimeout: 2 * time.Minute,
	}
}

// Validate checks the config for the combinations the replay cannot run with.
func (c *Config) Validate() error {
	if c.UpstreamURL == "" {
		return fmt.Errorf("no beacon upstream configured (--upstream): tracoor alone cannot resolve slots")
	}

	if c.TracoorURL != "" && c.TracoorNetwork == "" {
		return fmt.Errorf("--tracoor-network is required when --tracoor is set")
	}

	if c.StartSlot == 0 {
		return fmt.Errorf("no start slot configured (--start-slot / --start-epoch)")
	}

	if c.Speed < 0 {
		return fmt.Errorf("speed must not be negative")
	}

	if c.StepSettle <= 0 {
		c.StepSettle = 250 * time.Millisecond
	}

	if c.StateHoldTimeout <= 0 {
		c.StateHoldTimeout = 2 * time.Minute
	}

	return nil
}
