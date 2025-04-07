package types

import "context"

type BlockData struct {
	HeaderVersion uint64
	HeaderData    []byte
	BodyVersion   uint64
	BodyData      []byte
	Body          interface{}
}
type BlockDbEngine interface {
	Close() error
	GetBlock(ctx context.Context, slot uint64, root []byte, parseBlock func(uint64, []byte) (interface{}, error)) (*BlockData, error)
	AddBlock(ctx context.Context, slot uint64, root []byte, dataCb func() (*BlockData, error)) (bool, error)
}
