package types

type BlockDbEngine interface {
	Close() error
	GetBlockHeader(root []byte) ([]byte, uint64, error)
	GetBlockBody(root []byte, parser func(uint64, []byte) (interface{}, error)) (interface{}, error)
	AddBlockHeader(root []byte, version uint64, header []byte) (bool, error)
	AddBlockBody(root []byte, version uint64, block []byte) error
}
