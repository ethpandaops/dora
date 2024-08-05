package execution

type ClientStatus uint8

var (
	ClientStatusOnline        ClientStatus = 1
	ClientStatusOffline       ClientStatus = 2
	ClientStatusSynchronizing ClientStatus = 3
)

func (s ClientStatus) String() string {
	switch s {
	case ClientStatusOnline:
		return "online"
	case ClientStatusOffline:
		return "offline"
	case ClientStatusSynchronizing:
		return "synchronizing"
	}

	return "unknown"
}