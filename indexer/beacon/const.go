package beacon

import (
	"time"
)

const (
	BeaconHeaderRequestTimeout time.Duration = 10 * time.Second
	BeaconBodyRequestTimeout   time.Duration = 10 * time.Second
)
