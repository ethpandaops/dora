package consensus

type ClientStatus uint8

var (
	ClientStatusOnline        ClientStatus = 1
	ClientStatusOffline       ClientStatus = 2
	ClientStatusSynchronizing ClientStatus = 3
	ClientStatusOptimistic    ClientStatus = 4
)

func (s ClientStatus) String() string {
	switch s {
	case ClientStatusOnline:
		return "online"
	case ClientStatusOffline:
		return "offline"
	case ClientStatusSynchronizing:
		return "synchronizing"
	case ClientStatusOptimistic:
		return "optimistic"
	}

	return "unknown"
}
