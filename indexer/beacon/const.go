package beacon

import (
	"time"
)

const EtherGweiFactor = 1_000_000_000

// BeaconHeaderRequestTimeout is the timeout duration for beacon header requests.
const BeaconHeaderRequestTimeout time.Duration = 10 * time.Second

// BeaconBodyRequestTimeout is the timeout duration for beacon body requests.
const BeaconBodyRequestTimeout time.Duration = 10 * time.Second

// BeaconStateRequestTimeout is the timeout duration for beacon state requests.
const BeaconStateRequestTimeout time.Duration = 600 * time.Second
