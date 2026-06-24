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

// getDutiesKey builds the S3 object key for a per-epoch duties object.
// Format: {pathPrefix}/{firstSlot/10000}/{firstSlot_padded}_duties
// The shared tier folders and slot-padded name keep duties objects sorted
// alongside the epoch's block objects in S3 viewers.
func (e *S3Engine) getDutiesKey(firstSlot uint64) string {
	return path.Join(
		e.pathPrefix,
		fmt.Sprintf("%06d", firstSlot/10000),
		fmt.Sprintf("%010d_duties", firstSlot),
	)
}

// AddEpochDuties packs the resolved duties into a single DUTY object and stores it.
func (e *S3Engine) AddEpochDuties(ctx context.Context, duties *types.EpochDuties) (int64, error) {
	data, err := types.EncodeEpochDuties(duties)
	if err != nil {
		return 0, fmt.Errorf("failed to encode duties: %w", err)
	}

	key := e.getDutiesKey(duties.FirstSlot)
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
		return 0, fmt.Errorf("failed to upload duties: %w", err)
	}

	e.putBytes.Add(int64(len(data)))
	return int64(len(data)), nil
}

// GetEpochDuties retrieves and decodes the full duties object for an epoch.
func (e *S3Engine) GetEpochDuties(ctx context.Context, firstSlot uint64) (*types.EpochDuties, error) {
	key := e.getDutiesKey(firstSlot)
	e.getCount.Add(1)

	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get duties: %w", err)
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read duties: %w", err)
	}

	e.getBytes.Add(int64(len(data)))
	return types.DecodeEpochDuties(firstSlot, data)
}

// GetSlotCommittees reads only the requested slot's attester committees using
// two range reads (header + slot span).
func (e *S3Engine) GetSlotCommittees(ctx context.Context, firstSlot uint64, slot uint64) ([][]uint64, error) {
	key := e.getDutiesKey(firstSlot)

	header, err := e.readDutiesHeader(ctx, key)
	if err != nil || header == nil {
		return nil, err
	}

	slotIndex := slot - firstSlot
	if slotIndex >= header.SlotsPerEpoch {
		return nil, fmt.Errorf("slot %d out of range for epoch at %d", slot, firstSlot)
	}

	off, length := header.AttesterSlotRange(slotIndex)
	if length == 0 {
		return nil, nil
	}

	data, err := e.rangeRead(ctx, key, off, length)
	if err != nil || data == nil {
		return nil, err
	}

	return header.SplitSlotCommittees(slotIndex, data)
}

// GetSlotPtc reads only the requested slot's PTC members.
func (e *S3Engine) GetSlotPtc(ctx context.Context, firstSlot uint64, slot uint64) ([]uint64, error) {
	key := e.getDutiesKey(firstSlot)

	header, err := e.readDutiesHeader(ctx, key)
	if err != nil || header == nil {
		return nil, err
	}
	if header.PtcSize == 0 {
		return nil, nil
	}

	slotIndex := slot - firstSlot
	if slotIndex >= header.SlotsPerEpoch {
		return nil, fmt.Errorf("slot %d out of range for epoch at %d", slot, firstSlot)
	}

	off, length := header.PtcSlotRange(slotIndex)
	data, err := e.rangeRead(ctx, key, off, length)
	if err != nil || data == nil {
		return nil, err
	}

	return types.DecodeIndexList(data, header.IndexWidth), nil
}

// readDutiesHeader reads and decodes the fixed-size header of a duties object.
// Returns nil, nil if the object does not exist.
func (e *S3Engine) readDutiesHeader(ctx context.Context, key string) (*types.DutiesHeader, error) {
	data, err := e.rangeRead(ctx, key, 0, int64(types.DutiesHeaderSize))
	if err != nil || data == nil {
		return nil, err
	}
	return types.DecodeDutiesHeader(data)
}

// HasEpochDuties checks if a duties object exists for an epoch.
func (e *S3Engine) HasEpochDuties(ctx context.Context, firstSlot uint64) (bool, error) {
	key := e.getDutiesKey(firstSlot)
	e.statCount.Add(1)

	stat, err := e.client.StatObject(ctx, e.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat duties: %w", err)
	}

	return stat.Size > 0, nil
}

// PruneEpochDutiesBefore deletes duties objects for all epochs whose first slot
// is before maxFirstSlot, filtering on the _duties suffix.
func (e *S3Engine) PruneEpochDutiesBefore(ctx context.Context, maxFirstSlot uint64) (int64, error) {
	var totalDeleted int64

	maxTier := maxFirstSlot / 10000

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

				if !strings.HasSuffix(obj.Key, "_duties") {
					continue
				}

				if parseSlotFromKey(obj.Key) >= maxFirstSlot {
					continue
				}

				deleteCh <- obj
			}
		}()

		for err := range e.client.RemoveObjects(ctx, e.bucket, deleteCh, minio.RemoveObjectsOptions{}) {
			if err.Err != nil {
				return totalDeleted, fmt.Errorf(
					"failed to delete duties object %s: %w",
					err.ObjectName, err.Err,
				)
			}
			totalDeleted++
		}
	}

	return totalDeleted, nil
}
