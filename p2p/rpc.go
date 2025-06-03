package p2p

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/OffchainLabs/prysm/v6/beacon-chain/p2p"
	"github.com/OffchainLabs/prysm/v6/consensus-types/primitives"
	core "github.com/libp2p/go-libp2p-core"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/beacon-chain/p2p/encoder"
	"github.com/prysmaticlabs/prysm/beacon-chain/sync"
)

const PeerDAScolumns = 128

func (c *Client) Ping(ctx context.Context, pid peer.ID) (err error) {
	stream, err := r.host.NewStream(ctx, pid, r.protocolID(p2p.RPCPingTopicV1))
	if err != nil {
		return fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCPingTopicV1, pid, err)
	}
	defer stream.Reset()

	req := primitives.SSZUint64(uint64(1))
	if err := r.writeRequest(ctx, stream, &req); err != nil {
		return fmt.Errorf("write ping request: %w", err)
	}

	// read and decode status response
	resp := new(primitives.SSZUint64)
	if err := r.readResponse(ctx, stream, resp); err != nil {
		return fmt.Errorf("read ping response: %w", err)
	}

	// we have the data that we want, so ignore error here
	_ = stream.Close() // (both sides should actually be already closed)

	return nil
}

func (r *ReqResp) GoodBye(ctx context.Context, pid peer.ID) (err error) {
	stream, err := r.host.NewStream(ctx, pid, r.protocolID(p2p.RPCGoodByeTopicV1))
	if err != nil {
		return fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCGoodByeTopicV1, pid, err)
	}
	defer stream.Reset()

	req := primitives.SSZUint64(uint64(1))
	if err := r.writeRequest(ctx, stream, &req); err != nil {
		return fmt.Errorf("write goodbye request: %w", err)
	}

	// read and decode status response
	resp := new(primitives.SSZUint64)
	if err := r.readResponse(ctx, stream, resp); err != nil {
		return fmt.Errorf("read goodbye response: %w", err)
	}

	// we have the data that we want, so ignore error here
	_ = stream.Close() // (both sides should actually be already closed)

	return nil
}

func (r *ReqResp) Status(ctx context.Context, pid peer.ID) (status *pb.Status, err error) {
	stream, err := r.host.NewStream(ctx, pid, r.protocolID(p2p.RPCStatusTopicV1))
	if err != nil {
		return nil, fmt.Errorf("new stream to peer %s: %w", pid, err)
	}
	defer stream.Reset()

	if err := r.writeRequest(ctx, stream, &r.cfg.BeaconStatus); err != nil {
		return nil, fmt.Errorf("write status request: %w", err)
	}

	// read and decode status response
	resp := &pb.Status{}
	if err := r.readResponse(ctx, stream, resp); err != nil {
		return nil, fmt.Errorf("read status response: %w", err)
	}

	// we have the data that we want, so ignore error here
	_ = stream.Close() // (both sides should actually be already closed)

	return resp, nil
}

func (r *ReqResp) MetaDataV2(ctx context.Context, pid peer.ID) (resp *pb.MetaDataV2, err error) {
	stream, err := r.host.NewStream(ctx, pid, r.protocolID(RPCMetaDataV3))
	if err != nil {
		return resp, fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCMetaDataTopicV2, pid, err)
	}
	defer stream.Reset()

	if err := r.writeRequest(ctx, stream, &r.cfg.BeaconMetadata); err != nil {
		return nil, fmt.Errorf("write status request: %w", err)
	}

	// read and decode status response
	resp = &pb.MetaDataV2{}
	if err := r.readResponse(ctx, stream, resp); err != nil {
		return nil, fmt.Errorf("read metadata response: %w", err)
	}

	// we have the data that we want, so ignore error here
	_ = stream.Close() // (both sides should actually be already closed)

	return resp, nil
}

// block requests
func (r *ReqResp) RawBlocksByRangeV2(ctx context.Context, pid peer.ID, startSlot, finishSlot int64) ([]*pb.SignedBeaconBlockDeneb, error) {
	var err error

	blocks := make([]*pb.SignedBeaconBlockDeneb, 0)

	stream, err := r.host.NewStream(ctx, pid, r.protocolID(p2p.RPCBlocksByRangeTopicV2))
	if err != nil {
		return blocks, fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCMetaDataTopicV2, pid, err)
	}
	defer stream.Close()
	defer stream.Reset()

	req := &pb.BeaconBlocksByRangeRequest{
		StartSlot: primitives.Slot(startSlot),
		Count:     uint64(finishSlot - startSlot),
		Step:      1,
	}
	if err := r.writeRequest(ctx, stream, req); err != nil {
		return blocks, fmt.Errorf("write block_by_range request: %w", err)
	}

	// read and decode status response
	for i := uint64(0); ; i++ {
		isFirstChunk := i == 0
		_, err := r.readChunkedBlock(stream, &encoder.SszNetworkEncoder{}, isFirstChunk)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading block_by_range request: %w", err)
		}
	}
	return blocks, nil
}

func (r *ReqResp) BlocksByRangeV2(ctx context.Context, pid peer.ID, startSlot, finishSlot uint64) (time.Duration, []*pb.SignedBeaconBlockDeneb, error) {
	var err error
	blocks := make([]*pb.SignedBeaconBlockDeneb, 0)

	stream, err := r.host.NewStream(ctx, pid, r.protocolID(p2p.RPCBlocksByRangeTopicV2))
	if err != nil {
		return time.Duration(0), blocks, fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCMetaDataTopicV2, pid, err)
	}
	defer stream.Close()
	defer stream.Reset()

	req := &pb.BeaconBlocksByRangeRequest{
		StartSlot: primitives.Slot(startSlot),
		Count:     finishSlot - startSlot,
		Step:      1,
	}
	if err := r.writeRequest(ctx, stream, req); err != nil {
		return time.Duration(0), blocks, fmt.Errorf("write block_by_range request: %w", err)
	}

	tStart := time.Now()
	// read and decode status response
	for i := uint64(0); ; i++ {
		isFirstChunk := i == 0
		_, err := r.readChunkedBlock(stream, &encoder.SszNetworkEncoder{}, isFirstChunk)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return time.Duration(0), nil, fmt.Errorf("reading block_by_range request: %w", err)
		}
	}
	opDuration := time.Since(tStart)
	return opDuration, blocks, nil
}

// ReadChunkedBlock handles each response chunk that is sent by the
// peer and converts it into a beacon block.
// Adaptation from Prysm's -> https://github.com/prysmaticlabs/prysm/blob/2e29164582c3665cdf5a472cd4ec9838655c9754/beacon-chain/sync/rpc_chunked_response.go#L85
func (r *ReqResp) readChunkedBlock(stream core.Stream, encoding encoder.NetworkEncoding, isFirstChunk bool) (*pb.SignedBeaconBlockContentsElectra, error) {
	// Handle deadlines differently for first chunk
	if isFirstChunk {
		return r.readFirstChunkedBlock(stream, encoding)
	}
	return r.readResponseChunk(stream, encoding)
}

// readFirstChunkedBlock reads the first chunked block and applies the appropriate deadlines to it.
func (r *ReqResp) readFirstChunkedBlock(stream core.Stream, encoding encoder.NetworkEncoding) (*pb.SignedBeaconBlockContentsElectra, error) {
	// read status
	code, errMsg, err := sync.ReadStatusCode(stream, encoding)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("read RPC status errored %s", errMsg)
	}
	// set deadline for reading from stream
	if err = stream.SetWriteDeadline(time.Now().Add(r.cfg.WriteTimeout)); err != nil {
		return nil, fmt.Errorf("failed setting write deadline on stream: %w", err)
	}
	return r.decodeElectraBlock(encoding, stream)
}

// readResponseChunk reads the response from the stream and decodes it into the
// provided message type.
func (r *ReqResp) readResponseChunk(stream core.Stream, encoding encoder.NetworkEncoding) (*pb.SignedBeaconBlockContentsElectra, error) {
	if err := stream.SetWriteDeadline(time.Now().Add(r.cfg.WriteTimeout)); err != nil {
		return nil, fmt.Errorf("failed setting write deadline on stream: %w", err)
	}
	code, errMsg, err := sync.ReadStatusCode(stream, encoding)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("read RPC status errored %s", errMsg)
	}
	return r.decodeElectraBlock(encoding, stream)
}

// getBlockForForkVersion returns an ReadOnlySignedBeaconBlock interface from the block type of each ForkVersion
func (r *ReqResp) decodeElectraBlock(encoding encoder.NetworkEncoding, stream network.Stream) (blk *pb.SignedBeaconBlockContentsElectra, err error) {
	err = encoding.DecodeWithMaxLength(stream, blk)
	if err != nil {
		return blk, err
	}
	return blk, nil
}

// -- Data column requests --
// https://github.com/ethereum/consensus-specs/blob/dev/specs/fulu/p2p-interface.md#datacolumnsidecarsbyrange-v1
func (r *ReqResp) DataColumnByRangeV1(ctx context.Context, pid peer.ID, slot uint64, columnIdxs []uint64) (time.Duration, []*pb.DataColumnSidecar, error) {
	var err error
	dataColumns := make([]*pb.DataColumnSidecar, 0)

	chunks := uint64(1 * len(columnIdxs) * PeerDAScolumns)

	stream, err := r.host.NewStream(ctx, pid, r.protocolID(RPCDataColumnsByRangeV1))
	if err != nil {
		return time.Duration(0), dataColumns, fmt.Errorf("new %s stream to peer %s: %w", p2p.RPCMetaDataTopicV2, pid, err)
	}
	defer stream.Close()
	defer stream.Reset()

	req := &pb.DataColumnSidecarsByRangeRequest{
		StartSlot: primitives.Slot(slot),
		Count:     uint64(1),
		Columns:   columnIdxs,
	}
	if err := r.writeRequest(ctx, stream, req); err != nil {
		return time.Duration(0), dataColumns, fmt.Errorf("write data_columns_by_range request: %w", err)
	}

	tStart := time.Now()
	// read and decode status response

	for i := uint64(0); ; /* no stop condition */ i++ {
		dataCol, err := readChunkedDataColumnSideCar(stream, r.cfg.Encoder, r.cfg.BeaconStatus.ForkDigest)
		if errors.Is(err, io.EOF) {
			// End of stream.
			break
		}

		if dataCol == nil {
			return time.Duration(0), dataColumns, errors.Wrap(err, "validation error")
		}

		if err != nil {
			return time.Duration(0), dataColumns, errors.Wrap(err, "read chunked data column sidecar")
		}

		if i >= chunks {
			// The response MUST contain no more than `reqCount` blocks.
			// (`reqCount` is already capped by `maxRequestDataColumnSideCar`.)
			return time.Duration(0), dataColumns, errors.New("invalid - response contains more data column sidecars than requested")
		}

		dataColumns = append(dataColumns, dataCol)
	}
	opDuration := time.Since(tStart)
	return opDuration, dataColumns, nil
}

// -- new --

func readChunkedDataColumnSideCar(
	stream network.Stream,
	encoding encoder.NetworkEncoding,
	forkDigest []byte,
	// validation any, // TODO: to validate blob column

) (*pb.DataColumnSidecar, error) {
	// Read the status code from the stream.
	statusCode, errMessage, err := sync.ReadStatusCode(stream, encoding)
	if err != nil {
		return nil, errors.Wrap(err, "read status code")
	}

	if statusCode != 0 {
		return nil, errors.New("data column chunked read failure " + errMessage)
	}
	// Retrieve the fork digest.
	ctxBytes, err := readContextFromStream(stream)
	if err != nil {
		return nil, errors.Wrap(err, "read context from stream")
	}

	// Check if the fork digest is recognized.
	if string(ctxBytes) != string(forkDigest) {
		return nil, errors.Errorf("unrecognized fork digest %#x", ctxBytes)
	}

	// Decode the data column sidecar from the stream.
	dataColumnSidecar := new(pb.DataColumnSidecar)
	if err := encoding.DecodeWithMaxLength(stream, dataColumnSidecar); err != nil {
		return nil, errors.Wrap(err, "failed to decode the protobuf-encoded BlobSidecar message from RPC chunk stream")
	}

	// Run validation functions.
	/*
		for _, val := range validation {
			if !val(roDataColumn) {
				return nil, nil
			}
		}
	*/
	return dataColumnSidecar, nil
}

// reads any attached context-bytes to the payload.
func readContextFromStream(stream network.Stream) ([]byte, error) {
	hasCtx, err := expectRpcContext(stream)
	if err != nil {
		return nil, err
	}
	if !hasCtx {
		return []byte{}, nil
	}
	// Read context (fork-digest) from stream
	b := make([]byte, 4)
	if _, err := io.ReadFull(stream, b); err != nil {
		return nil, err
	}
	return b, nil
}

func expectRpcContext(stream network.Stream) (bool, error) {
	_, message, version, err := p2p.TopicDeconstructor(string(stream.Protocol()))
	if err != nil {
		return false, err
	}
	// For backwards compatibility, we want to omit context bytes for certain v1 methods that were defined before
	// context bytes were introduced into the protocol.
	if version == p2p.SchemaVersionV1 && p2p.OmitContextBytesV1[message] {
		return false, nil
	}
	return true, nil
}
