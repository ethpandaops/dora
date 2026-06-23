package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"path"
	"sync"

	cpebble "github.com/cockroachdb/pebble"
	"github.com/minio/minio-go/v7"

	dpebble "github.com/ethpandaops/dora/blockdb/pebble"
	btypes "github.com/ethpandaops/dora/blockdb/types"
)

// copyDutiesPass copies the per-epoch duties objects from source to target.
// Duties use different on-disk layouts per backend (S3: one packed object per
// epoch, Pebble: one key per slot), so they are copied through the engine-
// agnostic EpochDuties representation rather than as raw bytes.
func (c *blockdbCopier) copyDutiesPass(ctx context.Context) error {
	firstSlots, err := c.enumerateDuties(ctx)
	if err != nil {
		return fmt.Errorf("enumerate duties: %w", err)
	}

	if len(firstSlots) == 0 {
		return nil
	}

	c.logger.WithField("epochs", len(firstSlots)).Info("copying duties")

	work := make(chan uint64, c.threads*2)

	var wg sync.WaitGroup
	for i := 0; i < c.threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for firstSlot := range work {
				if err := c.copyDutiesEpoch(ctx, firstSlot); err != nil {
					c.errors.Add(1)
					c.logger.WithError(err).WithField("firstSlot", firstSlot).Error("copy duties failed")
				}
			}
		}()
	}

	for _, firstSlot := range firstSlots {
		work <- firstSlot
	}
	close(work)
	wg.Wait()

	return nil
}

// copyDutiesEpoch reads one epoch's duties from the source and writes it to the target.
func (c *blockdbCopier) copyDutiesEpoch(ctx context.Context, firstSlot uint64) error {
	duties, err := c.readDuties(ctx, firstSlot)
	if err != nil {
		return err
	}
	if duties == nil {
		c.objectsSkipped.Add(1)
		return nil
	}

	size, err := c.writeDuties(ctx, duties)
	if err != nil {
		return err
	}

	c.objectsCopied.Add(1)
	c.bytesCopied.Add(size)
	return nil
}

// enumerateDuties returns the first slots of all duties epochs in the source
// that fall within the configured slot range.
func (c *blockdbCopier) enumerateDuties(ctx context.Context) ([]uint64, error) {
	switch c.sourceEngine {
	case "s3":
		return c.enumerateDutiesS3(ctx)
	case "pebble":
		return c.enumerateDutiesPebble()
	}
	return nil, nil
}

func (c *blockdbCopier) enumerateDutiesS3(ctx context.Context) ([]uint64, error) {
	prefix := c.sourceS3Prefix
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	var firstSlots []uint64
	objectsCh := c.sourceS3.ListObjects(ctx, c.sourceS3Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for obj := range objectsCh {
		if obj.Err != nil {
			return nil, obj.Err
		}
		if len(obj.Key) < 7 || obj.Key[len(obj.Key)-7:] != "_duties" {
			continue
		}
		firstSlot := copyCmdParseSlotFromS3Key(obj.Key)
		if !c.slotInRange(firstSlot) {
			c.slotsOutOfRange.Add(1)
			continue
		}
		firstSlots = append(firstSlots, firstSlot)
	}
	return firstSlots, nil
}

func (c *blockdbCopier) enumerateDutiesPebble() ([]uint64, error) {
	lower := make([]byte, 2)
	binary.BigEndian.PutUint16(lower, dpebble.KeyNamespaceDuties)
	upper := make([]byte, 2)
	binary.BigEndian.PutUint16(upper, dpebble.KeyNamespaceDuties+1)

	iter, err := c.sourcePebble.NewIter(&cpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()

	var firstSlots []uint64
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != dpebble.DutiesKeyLen || key[10] != dpebble.DutiesRecordMeta {
			continue
		}
		firstSlot := binary.BigEndian.Uint64(key[2:10])
		if !c.slotInRange(firstSlot) {
			c.slotsOutOfRange.Add(1)
			continue
		}
		firstSlots = append(firstSlots, firstSlot)
	}
	return firstSlots, iter.Error()
}

// readDuties loads one epoch's duties from the source as an EpochDuties.
func (c *blockdbCopier) readDuties(ctx context.Context, firstSlot uint64) (*btypes.EpochDuties, error) {
	switch c.sourceEngine {
	case "s3":
		return c.readDutiesS3(ctx, firstSlot)
	case "pebble":
		return c.readDutiesPebble(firstSlot)
	}
	return nil, nil
}

func (c *blockdbCopier) readDutiesS3(ctx context.Context, firstSlot uint64) (*btypes.EpochDuties, error) {
	key := copyCmdBuildS3DutiesKey(c.sourceS3Prefix, firstSlot)
	obj, err := c.sourceS3.GetObject(ctx, c.sourceS3Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()

	data, err := io.ReadAll(obj)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, err
	}
	return btypes.DecodeEpochDuties(firstSlot, data)
}

func (c *blockdbCopier) readDutiesPebble(firstSlot uint64) (*btypes.EpochDuties, error) {
	meta, err := pebbleGet(c.sourcePebble, dpebble.MakeDutiesKey(firstSlot, dpebble.DutiesRecordMeta, 0))
	if err != nil || meta == nil {
		return nil, err
	}

	header, err := btypes.DecodeDutiesHeader(meta)
	if err != nil {
		return nil, err
	}

	d := &btypes.EpochDuties{
		FirstSlot:         firstSlot,
		Epoch:             header.Epoch,
		ValidatorCount:    header.ValidatorCount,
		SlotsPerEpoch:     header.SlotsPerEpoch,
		CommitteesPerSlot: header.CommitteesPerSlot,
		PtcSize:           header.PtcSize,
		Committees:        make([][][]uint64, header.SlotsPerEpoch),
	}
	if header.PtcSize > 0 {
		d.Ptc = make([][]uint64, header.SlotsPerEpoch)
	}

	for slotIndex := range header.SlotsPerEpoch {
		blob, err := pebbleGet(c.sourcePebble, dpebble.MakeDutiesKey(firstSlot, dpebble.DutiesRecordCommittees, uint16(slotIndex)))
		if err != nil {
			return nil, err
		}
		if blob != nil {
			committees, err := btypes.DecodeSlotCommittees(blob)
			if err != nil {
				return nil, err
			}
			d.Committees[slotIndex] = committees
		}

		if header.PtcSize > 0 {
			ptcBlob, err := pebbleGet(c.sourcePebble, dpebble.MakeDutiesKey(firstSlot, dpebble.DutiesRecordPtc, uint16(slotIndex)))
			if err != nil {
				return nil, err
			}
			if ptcBlob != nil {
				d.Ptc[slotIndex] = btypes.DecodeIndexList(ptcBlob, btypes.DutiesIndexWidth)
			}
		}
	}

	return d, nil
}

// writeDuties stores one epoch's duties in the target, returning bytes written.
func (c *blockdbCopier) writeDuties(ctx context.Context, d *btypes.EpochDuties) (int64, error) {
	switch c.targetEngine {
	case "s3":
		return c.writeDutiesS3(ctx, d)
	case "pebble":
		return c.writeDutiesPebble(d)
	}
	return 0, nil
}

func (c *blockdbCopier) writeDutiesS3(ctx context.Context, d *btypes.EpochDuties) (int64, error) {
	data, err := btypes.EncodeEpochDuties(d)
	if err != nil {
		return 0, err
	}
	key := copyCmdBuildS3DutiesKey(c.targetS3Prefix, d.FirstSlot)
	_, err = c.targetS3.PutObject(ctx, c.targetS3Bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (c *blockdbCopier) writeDutiesPebble(d *btypes.EpochDuties) (int64, error) {
	batch := c.targetPebble.NewBatch()
	defer func() { _ = batch.Close() }()

	var total int64

	meta := btypes.EncodeDutiesHeader(d)
	if err := batch.Set(dpebble.MakeDutiesKey(d.FirstSlot, dpebble.DutiesRecordMeta, 0), meta, nil); err != nil {
		return 0, err
	}
	total += int64(len(meta))

	for slotIndex, committees := range d.Committees {
		blob, err := btypes.EncodeSlotCommittees(committees)
		if err != nil {
			return 0, err
		}
		if err := batch.Set(dpebble.MakeDutiesKey(d.FirstSlot, dpebble.DutiesRecordCommittees, uint16(slotIndex)), blob, nil); err != nil {
			return 0, err
		}
		total += int64(len(blob))
	}

	if d.PtcSize > 0 {
		for slotIndex, ptc := range d.Ptc {
			blob, err := btypes.EncodeIndexList(ptc)
			if err != nil {
				return 0, err
			}
			if err := batch.Set(dpebble.MakeDutiesKey(d.FirstSlot, dpebble.DutiesRecordPtc, uint16(slotIndex)), blob, nil); err != nil {
				return 0, err
			}
			total += int64(len(blob))
		}
	}

	if err := batch.Commit(cpebble.Sync); err != nil {
		return 0, err
	}
	return total, nil
}

// copyCmdBuildS3DutiesKey builds the S3 object key for an epoch's duties object.
func copyCmdBuildS3DutiesKey(prefix string, firstSlot uint64) string {
	name := fmt.Sprintf("%010d_duties", firstSlot)
	tier := fmt.Sprintf("%06d", firstSlot/10000)
	if prefix == "" {
		return path.Join(tier, name)
	}
	return path.Join(prefix, tier, name)
}

// pebbleGet reads a key from a raw Pebble DB, returning a copy or nil if absent.
func pebbleGet(db *cpebble.DB, key []byte) ([]byte, error) {
	res, closer, err := db.Get(key)
	if err == cpebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()
	out := make([]byte, len(res))
	copy(out, res)
	return out, nil
}
