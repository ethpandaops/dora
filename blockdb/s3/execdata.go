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
)

// getExecDataKey builds the S3 object key for execution data.
// Format: {pathPrefix}/exec/{slot/10000}/{slot_padded}_{blockHashHex}
func (e *S3Engine) getExecDataKey(slot uint64, blockHash []byte) string {
	hashHex := hex.EncodeToString(blockHash[:4])
	return path.Join(
		e.pathPrefix,
		"exec",
		fmt.Sprintf("%06d", slot/10000),
		fmt.Sprintf("%010d_%s", slot, hashHex),
	)
}

// getExecDataDirPrefix returns the S3 prefix for a slot tier directory.
// Used for listing/pruning all objects in a slot range.
func (e *S3Engine) getExecDataDirPrefix(slotTier uint64) string {
	return path.Join(e.pathPrefix, "exec", fmt.Sprintf("%06d", slotTier)) + "/"
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
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get exec data: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read exec data: %w", err)
	}

	return data, nil
}

// GetExecDataRange retrieves a byte range of execution data using S3 Range header.
func (e *S3Engine) GetExecDataRange(ctx context.Context, slot uint64, blockHash []byte, offset int64, length int64) ([]byte, error) {
	key := e.getExecDataKey(slot, blockHash)

	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(offset, offset+length-1); err != nil {
		return nil, fmt.Errorf("failed to set range: %w", err)
	}

	obj, err := e.client.GetObject(ctx, e.bucket, key, opts)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get exec data range: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read exec data range: %w", err)
	}

	return data, nil
}

// HasExecData checks if execution data exists for a block.
func (e *S3Engine) HasExecData(ctx context.Context, slot uint64, blockHash []byte) (bool, error) {
	key := e.getExecDataKey(slot, blockHash)

	stat, err := e.client.StatObject(ctx, e.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
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
// Lists objects in slot-tier directories below the cutoff and batch-deletes them.
func (e *S3Engine) PruneExecDataBefore(ctx context.Context, maxSlot uint64) (int64, error) {
	var totalDeleted int64

	maxTier := maxSlot / 10000

	// Iterate through slot tiers from 0 up to (but not including) the maxSlot tier
	for tier := uint64(0); tier <= maxTier; tier++ {
		prefix := e.getExecDataDirPrefix(tier)

		objectsCh := e.client.ListObjects(ctx, e.bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		})

		// Collect objects to delete, filtering by slot for the boundary tier
		deleteCh := make(chan minio.ObjectInfo, 100)

		go func() {
			defer close(deleteCh)
			for obj := range objectsCh {
				if obj.Err != nil {
					continue
				}

				// For the boundary tier, parse the slot from the object key
				// to ensure we only delete objects below maxSlot
				if tier == maxTier {
					objSlot := parseSlotFromKey(obj.Key)
					if objSlot >= maxSlot {
						continue
					}
				}

				deleteCh <- obj
			}
		}()

		// Batch delete
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

// parseSlotFromKey extracts the slot number from an S3 object key.
// Expected format: .../NNNNNNNNNN_HHHHHHHH
// Returns 0 if parsing fails (safe default - won't match any pruning threshold).
func parseSlotFromKey(key string) uint64 {
	// Get the filename part (after last /)
	parts := strings.Split(key, "/")
	if len(parts) == 0 {
		return 0
	}
	filename := parts[len(parts)-1]

	// Split on underscore: NNNNNNNNNN_HHHHHHHH
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
