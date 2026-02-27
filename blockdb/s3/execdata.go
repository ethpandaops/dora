package s3

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/ethpandaops/dora/blockdb/types"
)

// getExecDataKey builds the S3 object key for execution data.
// Format: {pathPrefix}/{slot/10000}/{slot_padded}_{blockRootHex}_exec
func (e *S3Engine) getExecDataKey(slot uint64, blockRoot []byte) string {
	rootHex := hex.EncodeToString(blockRoot[:4])
	return path.Join(
		e.pathPrefix,
		fmt.Sprintf("%06d", slot/10000),
		fmt.Sprintf("%010d_%s_exec", slot, rootHex),
	)
}

// AddExecData stores execution data for a block. Returns stored size.
func (e *S3Engine) AddExecData(ctx context.Context, slot uint64, blockHash []byte, data []byte) (int64, error) {
	key := e.getExecDataKey(slot, blockHash)

	_, err := e.client.PutObject(
		ctx,
		e.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return 0, fmt.Errorf("failed to upload exec data: %w", err)
	}

	return int64(len(data)), nil
}

// GetExecData retrieves full execution data for a block.
func (e *S3Engine) GetExecData(ctx context.Context, slot uint64, blockHash []byte) ([]byte, error) {
	key := e.getExecDataKey(slot, blockHash)

	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get exec data: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read exec data: %w", err)
	}

	return data, nil
}

// GetExecDataRange retrieves a byte range of execution data using S3 Range header.
func (e *S3Engine) GetExecDataRange(ctx context.Context, slot uint64, blockHash []byte, offset int64, length int64) ([]byte, error) {
	key := e.getExecDataKey(slot, blockHash)
	return e.rangeRead(ctx, key, offset, length)
}

// GetExecDataTxSections retrieves all requested compressed sections for a
// single transaction using range reads. Three requests total:
//  1. Read first 28 bytes (header + tx count)
//  2. Read full index (tx count * 100 bytes)
//  3. Read contiguous data range covering all requested sections
func (e *S3Engine) GetExecDataTxSections(ctx context.Context, slot uint64, blockHash []byte, txHash []byte, sections uint32) (*types.ExecDataTxSections, error) {
	key := e.getExecDataKey(slot, blockHash)

	// Step 1: read header to get tx count (28 bytes)
	headerSize := int64(types.ExecDataMinHeaderSize())
	headerData, err := e.rangeRead(ctx, key, 0, headerSize)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec data header: %w", err)
	}
	if headerData == nil {
		return nil, nil
	}

	txCount, err := types.ParseExecDataTxCount(headerData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse exec data header: %w", err)
	}
	if txCount == 0 {
		return nil, nil
	}

	// Step 2: read full index (header already parsed the tx count from
	// the 28 bytes; now read the remaining tx entry bytes)
	fullIndexSize := int64(types.ExecDataIndexSize(txCount))
	indexData, err := e.rangeRead(ctx, key, headerSize, fullIndexSize-headerSize)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec data index: %w", err)
	}
	if indexData == nil {
		return nil, nil
	}

	// Reconstruct full index buffer and re-parse
	fullIndex := make([]byte, fullIndexSize)
	copy(fullIndex, headerData)
	copy(fullIndex[headerSize:], indexData)

	obj, err := types.ParseExecDataIndex(fullIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to parse exec data index: %w", err)
	}

	// Find the requested transaction
	txEntry := obj.FindTxEntry(txHash)
	if txEntry == nil {
		return nil, nil
	}

	// Check which requested sections actually exist
	effectiveMask := sections & txEntry.SectionsBitmap
	if effectiveMask == 0 {
		return &types.ExecDataTxSections{}, nil
	}

	// Step 3: read contiguous data range covering all requested sections
	spanOffset, spanLength := txEntry.GetTxSectionSpan(effectiveMask)
	if spanLength == 0 {
		return &types.ExecDataTxSections{}, nil
	}

	absOffset := int64(fullIndexSize) + int64(spanOffset)
	chunk, err := e.rangeRead(ctx, key, absOffset, int64(spanLength))
	if err != nil {
		return nil, fmt.Errorf("failed to read exec data sections: %w", err)
	}
	if chunk == nil {
		return nil, nil
	}

	// Slice individual sections from the contiguous chunk
	events, callTrace, stateChange, receiptMeta := txEntry.SliceTxSections(
		chunk, spanOffset, effectiveMask,
	)

	return &types.ExecDataTxSections{
		ReceiptMetaData: receiptMeta,
		EventsData:      events,
		CallTraceData:   callTrace,
		StateChangeData: stateChange,
	}, nil
}

// HasExecData checks if execution data exists for a block.
func (e *S3Engine) HasExecData(ctx context.Context, slot uint64, blockHash []byte) (bool, error) {
	key := e.getExecDataKey(slot, blockHash)

	stat, err := e.client.StatObject(ctx, e.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat exec data: %w", err)
	}

	return stat.Size > 0, nil
}

// DeleteExecData deletes execution data for a specific block.
func (e *S3Engine) DeleteExecData(ctx context.Context, slot uint64, blockHash []byte) error {
	key := e.getExecDataKey(slot, blockHash)

	return e.client.RemoveObject(ctx, e.bucket, key, minio.RemoveObjectOptions{})
}

// PruneExecDataBefore deletes execution data for all slots before maxSlot.
// Uses the shared tier prefix, filtering for _exec suffix.
func (e *S3Engine) PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	var totalDeleted int64

	maxTier := maxSlot / 10000

	for tier := uint64(0); tier <= maxTier; tier++ {
		prefix := path.Join(e.pathPrefix, fmt.Sprintf("%06d", tier)) + "/"

		objectsCh := e.client.ListObjects(ctx, e.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		})

		deleteCh := make(chan minio.ObjectInfo, 100)

		go func() {
			defer close(deleteCh)
			for obj := range objectsCh {
				if obj.Err != nil {
					continue
				}

				// Only process _exec objects
				if !strings.HasSuffix(obj.Key, "_exec") {
					continue
				}

				if tier == maxTier {
					objSlot := parseSlotFromKey(obj.Key)
					if objSlot >= maxSlot {
						continue
					}
				}

				deleteCh <- obj
			}
		}()

		for err := range e.client.RemoveObjects(ctx, e.bucket, deleteCh, minio.RemoveObjectsOptions{}) {
			if err.Err != nil {
				return totalDeleted, fmt.Errorf(
					"failed to delete exec data object %s: %w",
					err.ObjectName, err.Err,
				)
			}
			totalDeleted++
		}
	}

	return totalDeleted, nil
}

// rangeRead performs an S3 range read and returns the bytes.
// Returns nil, nil if the object is not found.
func (e *S3Engine) rangeRead(ctx context.Context, key string, offset int64, length int64) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+length-1); err != nil {
		return nil, fmt.Errorf("failed to set range: %w", err)
	}

	obj, err := e.client.GetObject(ctx, e.bucket, key, opts)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get range: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read range: %w", err)
	}

	return data, nil
}

// isNotFound checks if an error is an S3 "not found" response.
func isNotFound(err error) bool {
	return minio.ToErrorResponse(err).Code == "NoSuchKey"
}

// parseSlotFromKey extracts the slot number from an S3 object key.
// Expected format: .../NNNNNNNNNN_HHHHHHHH[_exec]
// Returns 0 if parsing fails (safe default - won't match any pruning threshold).
func parseSlotFromKey(key string) uint64 {
	parts := strings.Split(key, "/")
	if len(parts) == 0 {
		return 0
	}
	filename := parts[len(parts)-1]

	underscoreIdx := strings.Index(filename, "_")
	if underscoreIdx <= 0 {
		return 0
	}

	var slot uint64
	_, err := fmt.Sscanf(filename[:underscoreIdx], "%d", &slot)
	if err != nil {
		return 0
	}

	return slot
}
