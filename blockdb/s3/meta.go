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

// Suffixes of the per-slot meta object keys. Objects under the legacy suffix
// are still read and move to the current suffix when they are rewritten.
const (
	metaKeySuffix       = "_meta"
	legacyMetaKeySuffix = "_bids"
)

// getMetaKey builds the S3 object key for a per-slot meta object.
// Format: {pathPrefix}/{slot/10000}/{slot_padded}_meta
// The shared tier folders and slot-padded name keep meta objects sorted
// alongside the slot's block objects in S3 viewers.
func (e *S3Engine) getMetaKey(slot uint64) string {
	return e.getMetaKeyWithSuffix(slot, metaKeySuffix)
}

// getMetaKeyWithSuffix builds the S3 object key for a per-slot meta object
// with the given key suffix.
func (e *S3Engine) getMetaKeyWithSuffix(slot uint64, suffix string) string {
	return path.Join(
		e.pathPrefix,
		fmt.Sprintf("%06d", slot/10000),
		fmt.Sprintf("%010d%s", slot, suffix),
	)
}

// AddSlotMeta packs the slot's meta data into a single object and stores it.
// An object left under the legacy key is removed, as the new one replaces it.
func (e *S3Engine) AddSlotMeta(ctx context.Context, meta *types.SlotMeta) (int64, error) {
	data, err := types.EncodeSlotMeta(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to encode slot meta: %w", err)
	}

	key := e.getMetaKey(meta.Slot)
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
		return 0, fmt.Errorf("failed to upload slot meta: %w", err)
	}

	e.putBytes.Add(int64(len(data)))

	// Removing a key that does not exist succeeds, so no lookup is needed.
	legacyKey := e.getMetaKeyWithSuffix(meta.Slot, legacyMetaKeySuffix)
	if err := e.client.RemoveObject(ctx, e.bucket, legacyKey, minio.RemoveObjectOptions{}); err != nil {
		return int64(len(data)), fmt.Errorf("failed to remove legacy slot meta object: %w", err)
	}

	return int64(len(data)), nil
}

// GetSlotMeta retrieves the parts of the meta object for a slot selected by
// flags. With range requests enabled only the object prefix and the selected
// sections beyond it are fetched. An object that is not found under the
// current key is looked up under the legacy key.
func (e *S3Engine) GetSlotMeta(ctx context.Context, slot uint64, flags types.SlotMetaFlags) (*types.SlotMeta, error) {
	for _, suffix := range []string{metaKeySuffix, legacyMetaKeySuffix} {
		meta, err := e.getSlotMetaByKey(ctx, e.getMetaKeyWithSuffix(slot, suffix), flags)
		if err != nil || meta != nil {
			return meta, err
		}
	}

	return nil, nil
}

// getSlotMetaByKey retrieves the selected parts of the meta object stored
// under the given key. Returns nil, nil if not found.
func (e *S3Engine) getSlotMetaByKey(ctx context.Context, key string, flags types.SlotMetaFlags) (*types.SlotMeta, error) {
	if !e.config.EnableRangeRequests || !e.rangeRequestsEnabled {
		data, err := e.getMetaObject(ctx, key)
		if err != nil || data == nil {
			return nil, err
		}
		return types.DecodeSlotMetaSections(data, flags)
	}

	return types.ReadSlotMeta(flags, func(offset int64, length int64) ([]byte, error) {
		if length <= 0 {
			return e.getMetaObject(ctx, key)
		}
		return e.rangeRead(ctx, key, offset, length)
	})
}

// getMetaObject reads a complete meta object. Returns nil, nil if not found.
func (e *S3Engine) getMetaObject(ctx context.Context, key string) ([]byte, error) {
	e.getCount.Add(1)

	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get slot meta: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read slot meta: %w", err)
	}

	e.getBytes.Add(int64(len(data)))
	return data, nil
}

// PruneSlotMetaBefore deletes meta objects for all slots before maxSlot,
// filtering on the meta key suffixes.
func (e *S3Engine) PruneSlotMetaBefore(ctx context.Context, maxSlot uint64) (int64, error) {
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

				if !strings.HasSuffix(obj.Key, metaKeySuffix) && !strings.HasSuffix(obj.Key, legacyMetaKeySuffix) {
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
					"failed to delete slot meta object %s: %w",
					err.ObjectName, err.Err,
				)
			}
			totalDeleted++
		}
	}

	return totalDeleted, nil
}
