package beacon

import (
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

var nullRoot phase0.Root = phase0.Root{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

const (
	BeaconHeaderRequestTimeout time.Duration = 10 * time.Second
	BeaconBodyRequestTimeout   time.Duration = 10 * time.Second
)
