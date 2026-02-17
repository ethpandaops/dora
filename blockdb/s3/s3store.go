package s3

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Engine struct {
	client     *minio.Client
	bucket     string
	pathPrefix string
	config     dtypes.S3BlockDBConfig

	// Range request support (configured via EnableRangeRequests)
	rangeRequestsEnabled bool
}

func NewS3Engine(config dtypes.S3BlockDBConfig) (types.BlockDbEngine, error) {
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: bool(config.Secure),
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	// Check if bucket exists
	exists, err := client.BucketExists(context.Background(), config.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s does not exist", config.Bucket)
	}

	engine := &S3Engine{
		client:               client,
		bucket:               config.Bucket,
		pathPrefix:           strings.TrimPrefix(config.Path, "/"),
		config:               config,
		rangeRequestsEnabled: config.EnableRangeRequests,
	}

	return engine, nil
}

func (e *S3Engine) Close() error {
	return nil
}

func (e *S3Engine) getObjectKey(root []byte, slot uint64) string {
	rootHex := hex.EncodeToString(root[:4]) // First 4 bytes
	return path.Join(e.pathPrefix, fmt.Sprintf("%06d", slot/10000), fmt.Sprintf("%010d_%s", slot, rootHex))
}

// GetStoredComponents returns which components exist for a block by reading metadata.
func (e *S3Engine) GetStoredComponents(ctx context.Context, slot uint64, root []byte) (types.BlockDataFlags, error) {
	key := e.getObjectKey(root, slot)

	// Read just the metadata
	meta, err := e.readMetadata(ctx, key)
	if err != nil {
		return 0, err
	}
	if meta == nil {
		return 0, nil
	}

	return meta.storedFlags(), nil
}

// readMetadata reads object metadata using range request if enabled, otherwise full read.
func (e *S3Engine) readMetadata(ctx context.Context, key string) (*objectMetadata, error) {
	if e.config.EnableRangeRequests {
		meta, err := e.readMetadataWithRange(ctx, key)
		if err == nil {
			return meta, nil
		}
		// Fall through to full read on error
	}

	// Full read fallback
	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	buf := make([]byte, maxMetadataSize)
	n, err := obj.Read(buf)
	if (err != nil && err != io.EOF) || n == 0 {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	return readObjectMetadata(buf[:n])
}

// readMetadataWithRange reads metadata using HTTP Range request.
func (e *S3Engine) readMetadataWithRange(ctx context.Context, key string) (*objectMetadata, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, int64(maxMetadataSize-1)); err != nil {
		return nil, err
	}

	obj, err := e.client.GetObject(ctx, e.bucket, key, opts)
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get object with range: %w", err)
	}
	defer obj.Close()

	buf := make([]byte, maxMetadataSize)
	n, err := obj.Read(buf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read range: %w", err)
	}

	return readObjectMetadata(buf[:n])
}

// GetBlock retrieves block data with selective loading based on flags.
func (e *S3Engine) GetBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	key := e.getObjectKey(root, slot)

	// Try range-based loading if enabled
	if e.config.EnableRangeRequests && e.rangeRequestsEnabled {
		data, err := e.getBlockWithRanges(ctx, key, flags, parseBlock, parsePayload)
		if err == nil {
			return data, nil
		}
		// Fall through to full read on error
	}

	// Full read fallback
	return e.getBlockFull(ctx, key, flags, parseBlock, parsePayload)
}

// getBlockWithRanges uses a single range request for selective loading.
// Makes exactly 2 GET requests: one for metadata, one for all requested data.
func (e *S3Engine) getBlockWithRanges(
	ctx context.Context,
	key string,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	// First, get metadata (1 GET request)
	meta, err := e.readMetadataWithRange(ctx, key)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}

	// Calculate the single byte range spanning all requested components
	rangeStart, rangeEnd := meta.getDataRange(flags)
	if rangeStart < 0 {
		// No data to fetch
		return &types.BlockData{
			HeaderVersion:  uint64(meta.ObjVersion),
			BodyVersion:    uint64(meta.BodyVersion),
			PayloadVersion: uint64(meta.PayloadVersion),
			BalVersion:     uint64(meta.BalVersion),
		}, nil
	}

	// Fetch all requested data in a single GET request
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(rangeStart, rangeEnd); err != nil {
		return nil, err
	}

	obj, err := e.client.GetObject(ctx, e.bucket, key, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get object range: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object range: %w", err)
	}

	// Extract requested components from the fetched data
	return e.extractComponents(meta, flags, data, rangeStart, parseBlock, parsePayload)
}

// extractComponents extracts requested components from fetched data.
func (e *S3Engine) extractComponents(
	meta *objectMetadata,
	flags types.BlockDataFlags,
	data []byte,
	dataStartOffset int64,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	blockData := &types.BlockData{
		HeaderVersion:  uint64(meta.ObjVersion),
		BodyVersion:    uint64(meta.BodyVersion),
		PayloadVersion: uint64(meta.PayloadVersion),
		BalVersion:     uint64(meta.BalVersion),
	}

	// Extract header if requested
	if flags.Has(types.BlockDataFlagHeader) && meta.HeaderLength > 0 {
		start := int64(meta.headerOffset()) - dataStartOffset
		end := start + int64(meta.HeaderLength)
		if start >= 0 && end <= int64(len(data)) {
			blockData.HeaderData = data[start:end]
		}
	}

	// Extract body if requested
	if flags.Has(types.BlockDataFlagBody) && meta.BodyLength > 0 {
		start := int64(meta.bodyOffset()) - dataStartOffset
		end := start + int64(meta.BodyLength)
		if start >= 0 && end <= int64(len(data)) {
			bodyData := data[start:end]
			if parseBlock != nil {
				body, err := parseBlock(uint64(meta.BodyVersion), bodyData)
				if err != nil {
					return nil, fmt.Errorf("failed to parse body: %w", err)
				}
				blockData.Body = body
			} else {
				blockData.BodyData = bodyData
			}
		}
	}

	// Extract payload if requested (v2+)
	if flags.Has(types.BlockDataFlagPayload) && meta.PayloadLength > 0 && meta.ObjVersion >= 2 {
		start := int64(meta.payloadOffset()) - dataStartOffset
		end := start + int64(meta.PayloadLength)
		if start >= 0 && end <= int64(len(data)) {
			payloadData := data[start:end]
			if parsePayload != nil {
				payload, err := parsePayload(uint64(meta.PayloadVersion), payloadData)
				if err != nil {
					return nil, fmt.Errorf("failed to parse payload: %w", err)
				}
				blockData.Payload = payload
			} else {
				blockData.PayloadData = payloadData
			}
		}
	}

	// Extract BAL if requested (v2+)
	if flags.Has(types.BlockDataFlagBal) && meta.BalLength > 0 && meta.ObjVersion >= 2 {
		start := int64(meta.balOffset()) - dataStartOffset
		end := start + int64(meta.BalLength)
		if start >= 0 && end <= int64(len(data)) {
			blockData.BalData = data[start:end]
		}
	}

	return blockData, nil
}

// getBlockFull performs a full object read (fallback when range requests fail).
func (e *S3Engine) getBlockFull(
	ctx context.Context,
	key string,
	flags types.BlockDataFlags,
	parseBlock func(uint64, []byte) (any, error),
	parsePayload func(uint64, []byte) (any, error),
) (*types.BlockData, error) {
	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// Read entire object
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object: %w", err)
	}

	// Parse metadata
	meta, err := readObjectMetadata(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	blockData := &types.BlockData{
		HeaderVersion:  uint64(meta.ObjVersion),
		BodyVersion:    uint64(meta.BodyVersion),
		PayloadVersion: uint64(meta.PayloadVersion),
		BalVersion:     uint64(meta.BalVersion),
	}

	metaSize := meta.metadataSize()

	// Extract header if requested
	if flags.Has(types.BlockDataFlagHeader) && meta.HeaderLength > 0 {
		headerEnd := metaSize + int(meta.HeaderLength)
		if headerEnd <= len(data) {
			blockData.HeaderData = data[metaSize:headerEnd]
		}
	}

	// Extract body if requested
	if flags.Has(types.BlockDataFlagBody) && meta.BodyLength > 0 {
		bodyStart := metaSize + int(meta.HeaderLength)
		bodyEnd := bodyStart + int(meta.BodyLength)
		if bodyEnd <= len(data) {
			bodyData := data[bodyStart:bodyEnd]
			if parseBlock != nil {
				body, err := parseBlock(uint64(meta.BodyVersion), bodyData)
				if err != nil {
					return nil, fmt.Errorf("failed to parse body: %w", err)
				}
				blockData.Body = body
			} else {
				blockData.BodyData = bodyData
			}
		}
	}

	// Extract payload if requested (v2+)
	if flags.Has(types.BlockDataFlagPayload) && meta.PayloadLength > 0 && meta.ObjVersion >= 2 {
		payloadStart := metaSize + int(meta.HeaderLength) + int(meta.BodyLength)
		payloadEnd := payloadStart + int(meta.PayloadLength)
		if payloadEnd <= len(data) {
			payloadData := data[payloadStart:payloadEnd]
			if parsePayload != nil {
				payload, err := parsePayload(uint64(meta.PayloadVersion), payloadData)
				if err != nil {
					return nil, fmt.Errorf("failed to parse payload: %w", err)
				}
				blockData.Payload = payload
			} else {
				blockData.PayloadData = payloadData
			}
		}
	}

	// Extract BAL if requested (v3+)
	if flags.Has(types.BlockDataFlagBal) && meta.BalLength > 0 && meta.ObjVersion >= 2 {
		balStart := metaSize + int(meta.HeaderLength) + int(meta.BodyLength) + int(meta.PayloadLength)
		balEnd := balStart + int(meta.BalLength)
		if balEnd <= len(data) {
			blockData.BalData = data[balStart:balEnd]
		}
	}

	return blockData, nil
}

// AddBlock stores block data. Returns (added, updated, error).
func (e *S3Engine) AddBlock(
	ctx context.Context,
	slot uint64,
	root []byte,
	dataCb func() (*types.BlockData, error),
) (bool, bool, error) {
	key := e.getObjectKey(root, slot)

	// Check what components already exist
	existingMeta, err := e.readMetadata(ctx, key)
	if err != nil && err.Error() != "failed to get object: NoSuchKey" {
		// Ignore "not found" errors
		existingFlags, _ := e.GetStoredComponents(ctx, slot, root)
		if existingFlags == 0 {
			existingMeta = nil
		}
	}

	// Get the new data
	blockData, err := dataCb()
	if err != nil {
		return false, false, fmt.Errorf("failed to get block data: %w", err)
	}

	// Calculate what we already have
	var existingFlags types.BlockDataFlags
	if existingMeta != nil {
		existingFlags = existingMeta.storedFlags()
	}

	// Calculate what the new data provides
	var newFlags types.BlockDataFlags
	if len(blockData.HeaderData) > 0 {
		newFlags |= types.BlockDataFlagHeader
	}
	if len(blockData.BodyData) > 0 {
		newFlags |= types.BlockDataFlagBody
	}
	if blockData.PayloadVersion != 0 && len(blockData.PayloadData) > 0 {
		newFlags |= types.BlockDataFlagPayload
	}
	if blockData.BalVersion != 0 && len(blockData.BalData) > 0 {
		newFlags |= types.BlockDataFlagBal
	}

	// Check if we need to update (new data has more components)
	needsUpdate := (newFlags &^ existingFlags) != 0
	isNew := existingFlags == 0

	if !isNew && !needsUpdate {
		// Already have all the data
		return false, false, nil
	}

	// If updating, merge with existing data
	finalData := blockData
	if !isNew && needsUpdate {
		// Fetch existing data and merge
		existingData, err := e.GetBlock(ctx, slot, root, types.BlockDataFlagAll, nil, nil)
		if err == nil && existingData != nil {
			finalData = mergeBlockData(existingData, blockData)
		}
	}

	// Write object (v1 for pre-gloas, v2 for gloas+)
	metaBytes := writeObjectMetadata(finalData)

	// Calculate total size and build reader chain (avoids copying to concatenated buffer)
	totalSize := int64(len(metaBytes) + len(finalData.HeaderData) + len(finalData.BodyData))
	readers := []io.Reader{
		bytes.NewReader(metaBytes),
		bytes.NewReader(finalData.HeaderData),
		bytes.NewReader(finalData.BodyData),
	}

	if finalData.BodyVersion >= uint64(spec.DataVersionGloas) {
		totalSize += int64(len(finalData.PayloadData) + len(finalData.BalData))
		readers = append(readers,
			bytes.NewReader(finalData.PayloadData),
			bytes.NewReader(finalData.BalData),
		)
	}

	// Upload object using MultiReader to stream without extra buffer allocation
	_, err = e.client.PutObject(
		ctx,
		e.bucket,
		key,
		io.MultiReader(readers...),
		totalSize,
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return false, false, fmt.Errorf("failed to upload block: %w", err)
	}

	return isNew, !isNew && needsUpdate, nil
}

// mergeBlockData merges existing data with new data (new takes precedence for non-empty fields).
func mergeBlockData(existing, new *types.BlockData) *types.BlockData {
	result := &types.BlockData{}

	// Use new data if available, otherwise keep existing
	if len(new.HeaderData) > 0 {
		result.HeaderVersion = new.HeaderVersion
		result.HeaderData = new.HeaderData
	} else {
		result.HeaderVersion = existing.HeaderVersion
		result.HeaderData = existing.HeaderData
	}

	if len(new.BodyData) > 0 {
		result.BodyVersion = new.BodyVersion
		result.BodyData = new.BodyData
	} else {
		result.BodyVersion = existing.BodyVersion
		result.BodyData = existing.BodyData
	}

	if new.PayloadVersion != 0 && len(new.PayloadData) > 0 {
		result.PayloadVersion = new.PayloadVersion
		result.PayloadData = new.PayloadData
	} else {
		result.PayloadVersion = existing.PayloadVersion
		result.PayloadData = existing.PayloadData
	}

	if new.BalVersion != 0 && len(new.BalData) > 0 {
		result.BalVersion = new.BalVersion
		result.BalData = new.BalData
	} else {
		result.BalVersion = existing.BalVersion
		result.BalData = existing.BalData
	}

	return result
}
