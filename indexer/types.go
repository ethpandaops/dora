package indexer

type HeadFork struct {
	Slot         uint64
	Root         []byte
	ReadyClients []*ConsensusClient
	AllClients   []*ConsensusClient
}
