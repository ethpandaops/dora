package beacon

import (
	"time"
)

const (
	BeaconHeaderRequestTimeout time.Duration = 10 * time.Second
	BeaconBodyRequestTimeout   time.Duration = 10 * time.Second
	ProposerDutyRequestTimeout time.Duration = 600 * time.Second
	AttesterDutyRequestTimeout time.Duration = 600 * time.Second
	BeaconStateRequestTimeout  time.Duration = 600 * time.Second
)
