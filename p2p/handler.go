package p2p

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	dynssz "github.com/pk910/dynamic-ssz"
)

type P2PHandler func(context.Context, *Client, network.Stream) error

const (
	rpcProtocolPrefix             = "/eth2/beacon_chain/req"
	rpcTopicPingV1                = rpcProtocolPrefix + "/ping/1"
	rpcTopicGoodByeV1             = rpcProtocolPrefix + "/goodbye/1"
	rpcTopicStatusV1              = rpcProtocolPrefix + "/status/1"
	rpcTopicMetaDataV1            = rpcProtocolPrefix + "/metadata/1"
	rpcTopicMetaDataV2            = rpcProtocolPrefix + "/metadata/2"
	rpcTopicMetaDataV3            = rpcProtocolPrefix + "/metadata/3"
	rpcTopicBlocksByRootV1        = rpcProtocolPrefix + "/blocks_by_root/1"
	rpcTopicBlocksByRootV2        = rpcProtocolPrefix + "/blocks_by_root/2"
	rpcTopicBlocksByRangeV1       = rpcProtocolPrefix + "/blocks_by_range/1"
	rpcTopicBlocksByRangeV2       = rpcProtocolPrefix + "/blocks_by_range/2"
	rpcTopicBlobSidecarsByRangeV1 = rpcProtocolPrefix + "/blob_sidecars_by_range/1"
	rpcTopicBlobSidecarsByRootV1  = rpcProtocolPrefix + "/blob_sidecars_by_root/1"
	rpcTopicDataColumnsByRangeV1  = rpcProtocolPrefix + "/data_columns_by_range/1"
	rpcTopicDataColumnsByRootV1   = rpcProtocolPrefix + "/data_columns_by_root/1"
)

type HandlerConfig struct {
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Handlers struct {
	cfg    HandlerConfig
	dynssz *dynssz.DynSsz
	funcs  map[string]P2PHandler
}

func NewHandlers(cfg HandlerConfig, dynssz *dynssz.DynSsz) *Handlers {
	h := &Handlers{
		cfg:    cfg,
		dynssz: dynssz,
	}

	h.funcs = map[string]P2PHandler{
		rpcTopicPingV1:                h.pingHandler,
		rpcTopicGoodByeV1:             h.goodbyeHandler,
		rpcTopicStatusV1:              h.dummyHandler,
		rpcTopicMetaDataV1:            h.dummyHandler,
		rpcTopicMetaDataV2:            h.dummyHandler,
		rpcTopicMetaDataV3:            h.dummyHandler,
		rpcTopicBlocksByRootV1:        h.dummyHandler,
		rpcTopicBlocksByRootV2:        h.dummyHandler,
		rpcTopicBlocksByRangeV1:       h.dummyHandler,
		rpcTopicBlocksByRangeV2:       h.dummyHandler,
		rpcTopicBlobSidecarsByRangeV1: h.dummyHandler,
		rpcTopicBlobSidecarsByRootV1:  h.dummyHandler,
		rpcTopicDataColumnsByRangeV1:  h.dummyHandler,
		rpcTopicDataColumnsByRootV1:   h.dummyHandler,
	}

	return h
}

func (h *Handlers) readRequest(ctx context.Context, stream network.Stream, output interface{}) (err error) {
	if err = stream.SetReadDeadline(time.Now().Add(h.cfg.ReadTimeout)); err != nil {
		return fmt.Errorf("failed setting read deadline on stream: %w", err)
	}

	data, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("failed reading stream: %w", err)
	}

	if err = stream.CloseRead(); err != nil {
		return fmt.Errorf("failed to close reading side of stream: %w", err)
	}

	if err = h.dynssz.UnmarshalSSZ(output, data); err != nil {
		return fmt.Errorf("unmarshal data: %w", err)
	}

	return nil
}

func (h *Handlers) readResponse(ctx context.Context, stream network.Stream, output interface{}) (err error) {
	if err = stream.SetReadDeadline(time.Now().Add(h.cfg.ReadTimeout)); err != nil {
		return fmt.Errorf("failed setting read deadline on stream: %w", err)
	}

	code := make([]byte, 1)
	if _, err := io.ReadFull(stream, code); err != nil {
		return fmt.Errorf("failed reading response code: %w", err)
	}

	// code == 0 means success
	// code != 0 means error
	if int(code[0]) != 0 {
		errData, err := io.ReadAll(stream)
		if err != nil {
			return fmt.Errorf("failed reading error data (code %d): %w", int(code[0]), err)
		}

		return fmt.Errorf("received error response (code %d): %s", int(code[0]), string(errData))
	}

	data, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("failed reading stream: %w", err)
	}

	if err = h.dynssz.UnmarshalSSZ(output, data); err != nil {
		return fmt.Errorf("unmarshal data: %w", err)
	}

	if err = stream.CloseRead(); err != nil {
		return fmt.Errorf("failed to close reading side of stream: %w", err)
	}

	return nil
}

func (h *Handlers) writeRequest(ctx context.Context, stream network.Stream, data interface{}) (err error) {
	if err = stream.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout)); err != nil {
		return fmt.Errorf("failed setting write deadline on stream: %w", err)
	}

	dataBytes, err := h.dynssz.MarshalSSZ(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	if _, err = stream.Write(dataBytes); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	if err = stream.CloseWrite(); err != nil {
		return fmt.Errorf("failed to close writing side of stream: %w", err)
	}

	return nil
}

func (h *Handlers) writeResponse(ctx context.Context, stream network.Stream, data interface{}) (err error) {
	if err = stream.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout)); err != nil {
		return fmt.Errorf("failed setting write deadline on stream: %w", err)
	}

	if _, err := stream.Write([]byte{0}); err != nil { // success response
		return fmt.Errorf("write success response code: %w", err)
	}

	dataBytes, err := h.dynssz.MarshalSSZ(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	if _, err = stream.Write(dataBytes); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	if err = stream.CloseWrite(); err != nil {
		return fmt.Errorf("failed to close writing side of stream: %w", err)
	}

	return nil
}

func (h *Handlers) dummyHandler(ctx context.Context, client *Client, stream network.Stream) error {
	return stream.Reset()
}

func (h *Handlers) pingHandler(ctx context.Context, client *Client, stream network.Stream) error {
	req := uint64(0)
	if err := h.readRequest(ctx, stream, &req); err != nil {
		return fmt.Errorf("read sequence number: %w", err)
	}

	sq := uint64(23)
	if err := h.writeResponse(ctx, stream, &sq); err != nil {
		return fmt.Errorf("write sequence number: %w", err)
	}
	return stream.Close()
}

func (h *Handlers) goodbyeHandler(ctx context.Context, client *Client, stream network.Stream) error {
	req := uint64(0)
	if err := h.readRequest(ctx, stream, &req); err != nil {
		return fmt.Errorf("read sequence number: %w", err)
	}
	client.logger.Warnf("received GoodBye from %s", stream.Conn().RemotePeer().String())
	return stream.Close()
}
