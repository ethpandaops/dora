package indexer

type HeadFork struct {
	Slot    uint64
	Root    []byte
	Clients []*IndexerClient
}
