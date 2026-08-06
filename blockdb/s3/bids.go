package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/ethpandaops/dora/blockdb/types"
)

// getBidsKey builds the S3 object key for a per-slot bids object.
// Format: {pathPrefix}/{slot/10000}/{slot_padded}_bids
// The shared tier folders and slot-padded name keep bids objects sorted
// alongside the slot's block objects in S3 viewers.
func (e *S3Engine) getBidsKey(slot uint64) string {
	return path.Join(
		e.pathPrefix,
		fmt.Sprintf("%06d", slot/10000),
		fmt.Sprintf("%010d_bids", slot),
	)
}

// AddSlotBids packs the slot's bids into a single BIDS object and stores it.
func (e *S3Engine) AddSlotBids(ctx context.Context, bids *types.SlotBids) (int64, error) {
	data, err := types.EncodeSlotBids(bids)
	if err != nil {
		return 0, fmt.Errorf("failed to encode bids: %w", err)
	}

	key := e.getBidsKey(bids.Slot)
	e.putCount.Add(1)

	_, err = e.client.PutObject(
		ctx,
		e.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return 0, fmt.Errorf("failed to upload bids: %w", err)
	}

	e.putBytes.Add(int64(len(data)))
	return int64(len(data)), nil
}

// GetSlotBids retrieves and decodes the bids object for a slot.
func (e *S3Engine) GetSlotBids(ctx context.Context, slot uint64) (*types.SlotBids, error) {
	key := e.getBidsKey(slot)
	e.getCount.Add(1)

	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get bids: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read bids: %w", err)
	}

	e.getBytes.Add(int64(len(data)))
	return types.DecodeSlotBids(data)
}

// PruneSlotBidsBefore deletes bids objects for all slots before maxSlot,
// filtering on the _bids suffix.
func (e *S3Engine) PruneSlotBidsBefore(ctx context.Context, maxSlot uint64) (int64, error) {
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

				if !strings.HasSuffix(obj.Key, "_bids") {
					continue
				}

				if parseSlotFromKey(obj.Key) >= maxSlot {
					continue
				}

				deleteCh <- obj
			}
		}()

		for err := range e.client.RemoveObjects(ctx, e.bucket, deleteCh, minio.RemoveObjectsOptions{}) {
			if err.Err != nil {
				return totalDeleted, fmt.Errorf(
					"failed to delete bids object %s: %w",
					err.ObjectName, err.Err,
				)
			}
			totalDeleted++
		}
	}

	return totalDeleted, nil
}
