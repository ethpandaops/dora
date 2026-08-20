package replay

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

// forkOrder lists the forks oldest first, paired with the spec key that activates them.
// The names are the ones the beacon API reports in Eth-Consensus-Version.
var forkOrder = []struct {
	name    string
	specKey string
}{
	{name: "altair", specKey: "ALTAIR_FORK_EPOCH"},
	{name: "bellatrix", specKey: "BELLATRIX_FORK_EPOCH"},
	{name: "capella", specKey: "CAPELLA_FORK_EPOCH"},
	{name: "deneb", specKey: "DENEB_FORK_EPOCH"},
	{name: "electra", specKey: "ELECTRA_FORK_EPOCH"},
	{name: "fulu", specKey: "FULU_FORK_EPOCH"},
	{name: "gloas", specKey: "GLOAS_FORK_EPOCH"},
	{name: "heze", specKey: "HEZE_FORK_EPOCH"},
}

type forkActivation struct {
	name  string
	epoch uint64
}

// chainInfo holds the genesis and timing parameters the replay needs to translate
// between slots and (virtual) wall clock time. Genesis stays the real one, so every
// timestamp the explorer renders is the true historical time of the replayed slot.
type chainInfo struct {
	genesisTime   time.Time
	slotDuration  time.Duration
	slotsPerEpoch uint64
	forks         []forkActivation
}

// bidsActiveAt reports whether a block at this slot can carry an execution payload bid,
// which only exists from the Gloas fork (EIP-7732) onwards. Before that, fetching the
// block to look for one would be a wasted round trip on every slot.
func (c *chainInfo) bidsActiveAt(slot uint64) bool {
	epoch := c.epochOf(slot)

	for _, fork := range c.forks {
		if fork.name == "gloas" {
			return epoch >= fork.epoch
		}
	}

	return false
}

// forkAt returns the fork a slot belongs to, as reported in Eth-Consensus-Version.
// Artifacts served from tracoor carry no such header of their own, and without it a
// client cannot tell which SSZ container it received.
func (c *chainInfo) forkAt(slot uint64) string {
	epoch := c.epochOf(slot)
	name := "phase0"

	for _, fork := range c.forks {
		if epoch >= fork.epoch {
			name = fork.name
		}
	}

	return name
}

func (c *chainInfo) slotTime(slot uint64) time.Time {
	return c.genesisTime.Add(time.Duration(slot) * c.slotDuration)
}

func (c *chainInfo) epochOf(slot uint64) uint64 {
	return slot / c.slotsPerEpoch
}

// loadChainInfo reads genesis and the timing specs from the beacon upstream.
func loadChainInfo(ctx context.Context, up *upstream) (*chainInfo, error) {
	genesisRsp := struct {
		Data struct {
			GenesisTime string `json:"genesis_time"`
		} `json:"data"`
	}{}

	if err := up.getJSON(ctx, "/eth/v1/beacon/genesis", &genesisRsp); err != nil {
		return nil, fmt.Errorf("error fetching genesis: %w", err)
	}

	genesisUnix, err := strconv.ParseInt(genesisRsp.Data.GenesisTime, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid genesis_time %q: %w", genesisRsp.Data.GenesisTime, err)
	}

	specRsp := struct {
		Data map[string]any `json:"data"`
	}{}

	if err := up.getJSON(ctx, "/eth/v1/config/spec", &specRsp); err != nil {
		return nil, fmt.Errorf("error fetching config spec: %w", err)
	}

	slotDurationMs, err := specUint(specRsp.Data, "SLOT_DURATION_MS")
	if err != nil {
		secondsPerSlot, secErr := specUint(specRsp.Data, "SECONDS_PER_SLOT")
		if secErr != nil {
			return nil, fmt.Errorf("could not determine slot duration: %w", err)
		}

		slotDurationMs = secondsPerSlot * 1000
	}

	slotsPerEpoch, err := specUint(specRsp.Data, "SLOTS_PER_EPOCH")
	if err != nil {
		return nil, err
	}

	if slotDurationMs == 0 || slotsPerEpoch == 0 {
		return nil, fmt.Errorf("upstream reported a zero slot duration or epoch length")
	}

	return &chainInfo{
		genesisTime:   time.Unix(genesisUnix, 0).UTC(),
		slotDuration:  time.Duration(slotDurationMs) * time.Millisecond,
		slotsPerEpoch: slotsPerEpoch,
		forks:         parseForkSchedule(specRsp.Data),
	}, nil
}

// parseForkSchedule reads the activation epoch of every fork the upstream knows about,
// in activation order. Forks that are not scheduled are left out.
func parseForkSchedule(specs map[string]any) []forkActivation {
	forks := make([]forkActivation, 0, len(forkOrder))

	for _, fork := range forkOrder {
		epoch, err := specUint(specs, fork.specKey)
		if err != nil {
			continue
		}

		forks = append(forks, forkActivation{name: fork.name, epoch: epoch})
	}

	return forks
}

// specUint reads a numeric spec value. The config endpoint reports numbers as decimal
// strings, but not every entry is a number (the blob schedule is a list), so anything
// that is not a plain numeric string is reported as an error rather than coerced.
func specUint(specs map[string]any, key string) (uint64, error) {
	raw, ok := specs[key]
	if !ok {
		return 0, fmt.Errorf("spec value %v missing", key)
	}

	text, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("spec value %v is not a number (got %T)", key, raw)
	}

	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid spec value %v=%q: %w", key, text, err)
	}

	return value, nil
}

// SlotsPerEpoch reads just the epoch length from a beacon node, so a start epoch given
// on the command line can be converted to a slot before the replay is constructed.
func SlotsPerEpoch(ctx context.Context, upstreamURL string) (uint64, error) {
	up, err := newUpstream(logrus.New(), &Config{UpstreamURL: upstreamURL})
	if err != nil {
		return 0, err
	}

	rsp := struct {
		Data map[string]any `json:"data"`
	}{}

	if err := up.getJSON(ctx, "/eth/v1/config/spec", &rsp); err != nil {
		return 0, fmt.Errorf("error fetching config spec: %w", err)
	}

	return specUint(rsp.Data, "SLOTS_PER_EPOCH")
}
