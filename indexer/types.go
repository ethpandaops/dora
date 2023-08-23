package indexer

type HeadFork struct {
	Slot         uint64
	Root         []byte
	ReadyClients []*IndexerClient
	AllClients   []*IndexerClient
}
