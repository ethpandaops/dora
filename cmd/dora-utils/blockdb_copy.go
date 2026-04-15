package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cpebble "github.com/cockroachdb/pebble"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// Pebble key namespace and format constants (matching blockdb/pebble).
const (
	pebbleNsBlock    uint16 = 1 // [ns:2][root:32][type:2] = 36 bytes
	pebbleNsExecData uint16 = 2 // [ns:2][slot:8][hash:4]  = 14 bytes
	// LRU also uses ns=2 but with 34-byte keys: [ns:2][root:32]

	pebbleBlockTypeHeader  uint16 = 1
	pebbleBlockTypeBody    uint16 = 2
	pebbleBlockTypePayload uint16 = 3
	pebbleBlockTypeBal     uint16 = 4

	pebbleValueHeaderSize = 16 // [version:8][timestamp:8]
)

// S3 block object metadata sizes (matching blockdb/s3/format.go).
const (
	s3MetaSizeV1 = 16 // [objVer:4][headerLen:4][bodyVer:4][bodyLen:4]
	s3MetaSizeV2 = 32 // v1 + [payloadVer:4][payloadLen:4][balVer:4][balLen:4]
)

var blockdbCopyCmd = &cobra.Command{
	Use:   "blockdb-copy",
	Short: "Copy blockdb data between pebble and S3 backends",
	Long: `Copy block database data between storage backends.

Supports all backend combinations: S3<->S3, Pebble<->Pebble, S3<->Pebble.
Cross-backend block copies work by computing the block root from the SSZ
header via HashTreeRoot and extracting the slot from the header message.

Examples:
  # S3 to S3 (migrate providers)
  dora-utils blockdb-copy \
    --source-engine s3 --source-s3-endpoint old.example.com \
    --source-s3-bucket blocks --source-s3-access-key KEY --source-s3-secret-key SECRET \
    --target-engine s3 --target-s3-endpoint new.example.com \
    --target-s3-bucket blocks --target-s3-access-key KEY --target-s3-secret-key SECRET

  # S3 to Pebble (full local copy)
  dora-utils blockdb-copy \
    --source-engine s3 --source-s3-endpoint s3.example.com \
    --source-s3-bucket blocks --source-s3-path dora/mainnet \
    --target-engine pebble --target-pebble-path /data/blockdb

  # Pebble to Pebble
  dora-utils blockdb-copy \
    --source-engine pebble --source-pebble-path /data/blockdb-old \
    --target-engine pebble --target-pebble-path /data/blockdb-new`,
	RunE: runBlockdbCopy,
}

func init() {
	rootCmd.AddCommand(blockdbCopyCmd)

	// Source flags
	blockdbCopyCmd.Flags().String("source-engine", "", "Source engine type (pebble/s3)")
	blockdbCopyCmd.Flags().String("source-pebble-path", "", "Source Pebble database path")
	blockdbCopyCmd.Flags().String("source-s3-endpoint", "", "Source S3 endpoint")
	blockdbCopyCmd.Flags().String("source-s3-bucket", "", "Source S3 bucket")
	blockdbCopyCmd.Flags().String("source-s3-access-key", "", "Source S3 access key")
	blockdbCopyCmd.Flags().String("source-s3-secret-key", "", "Source S3 secret key")
	blockdbCopyCmd.Flags().String("source-s3-region", "", "Source S3 region")
	blockdbCopyCmd.Flags().String("source-s3-path", "", "Source S3 path prefix")
	blockdbCopyCmd.Flags().Bool("source-s3-secure", false, "Source S3 use TLS")

	// Target flags
	blockdbCopyCmd.Flags().String("target-engine", "", "Target engine type (pebble/s3)")
	blockdbCopyCmd.Flags().String("target-pebble-path", "", "Target Pebble database path")
	blockdbCopyCmd.Flags().String("target-s3-endpoint", "", "Target S3 endpoint")
	blockdbCopyCmd.Flags().String("target-s3-bucket", "", "Target S3 bucket")
	blockdbCopyCmd.Flags().String("target-s3-access-key", "", "Target S3 access key")
	blockdbCopyCmd.Flags().String("target-s3-secret-key", "", "Target S3 secret key")
	blockdbCopyCmd.Flags().String("target-s3-region", "", "Target S3 region")
	blockdbCopyCmd.Flags().String("target-s3-path", "", "Target S3 path prefix")
	blockdbCopyCmd.Flags().Bool("target-s3-secure", false, "Target S3 use TLS")

	// Copy options
	blockdbCopyCmd.Flags().IntP("threads", "j", 10, "Number of concurrent copy workers")
	blockdbCopyCmd.Flags().Bool("no-blocks", false, "Skip block data")
	blockdbCopyCmd.Flags().Bool("no-execdata", false, "Skip execution data")
	blockdbCopyCmd.Flags().Int64("min-slot", -1, "Minimum slot to copy (inclusive, -1 = no limit)")
	blockdbCopyCmd.Flags().Int64("max-slot", -1, "Maximum slot to copy (inclusive, -1 = no limit)")
	blockdbCopyCmd.Flags().BoolP("verbose", "v", false, "Verbose output")

	if err := blockdbCopyCmd.MarkFlagRequired("source-engine"); err != nil {
		panic(err)
	}

	if err := blockdbCopyCmd.MarkFlagRequired("target-engine"); err != nil {
		panic(err)
	}
}

func runBlockdbCopy(cmd *cobra.Command, _ []string) error {
	sourceEngine, _ := cmd.Flags().GetString("source-engine")
	targetEngine, _ := cmd.Flags().GetString("target-engine")
	threads, _ := cmd.Flags().GetInt("threads")
	noBlocks, _ := cmd.Flags().GetBool("no-blocks")
	noExecdata, _ := cmd.Flags().GetBool("no-execdata")
	minSlot, _ := cmd.Flags().GetInt64("min-slot")
	maxSlot, _ := cmd.Flags().GetInt64("max-slot")
	verbose, _ := cmd.Flags().GetBool("verbose")

	logger := logrus.New()
	if verbose {
		logger.SetLevel(logrus.DebugLevel)
	}

	if sourceEngine != "pebble" && sourceEngine != "s3" {
		return fmt.Errorf("--source-engine must be 'pebble' or 's3', got %q", sourceEngine)
	}

	if targetEngine != "pebble" && targetEngine != "s3" {
		return fmt.Errorf("--target-engine must be 'pebble' or 's3', got %q", targetEngine)
	}

	if noBlocks && noExecdata {
		return fmt.Errorf("--no-blocks and --no-execdata cannot both be set")
	}

	if threads < 1 {
		return fmt.Errorf("--threads must be at least 1")
	}

	copier := &blockdbCopier{
		logger:       logger.WithField("component", "blockdb-copy"),
		threads:      threads,
		copyBlocks:   !noBlocks,
		copyExec:     !noExecdata,
		minSlot:      minSlot,
		maxSlot:      maxSlot,
		sourceEngine: sourceEngine,
		targetEngine: targetEngine,
	}

	// Initialize source.
	switch sourceEngine {
	case "s3":
		client, bucket, prefix, err := newCopyS3Client(cmd, "source")
		if err != nil {
			return fmt.Errorf("source S3: %w", err)
		}

		copier.sourceS3 = client
		copier.sourceS3Bucket = bucket
		copier.sourceS3Prefix = prefix
	case "pebble":
		sourcePath, _ := cmd.Flags().GetString("source-pebble-path")
		if sourcePath == "" {
			return fmt.Errorf("--source-pebble-path is required")
		}

		cache := cpebble.NewCache(32 * 1024 * 1024)

		db, err := cpebble.Open(sourcePath, &cpebble.Options{
			ReadOnly: true,
			Cache:    cache,
		})

		cache.Unref()

		if err != nil {
			return fmt.Errorf("open source pebble %q: %w", sourcePath, err)
		}

		defer db.Close()

		copier.sourcePebble = db
	}

	// Initialize target.
	switch targetEngine {
	case "s3":
		client, bucket, prefix, err := newCopyS3Client(cmd, "target")
		if err != nil {
			return fmt.Errorf("target S3: %w", err)
		}

		copier.targetS3 = client
		copier.targetS3Bucket = bucket
		copier.targetS3Prefix = prefix
	case "pebble":
		targetPath, _ := cmd.Flags().GetString("target-pebble-path")
		if targetPath == "" {
			return fmt.Errorf("--target-pebble-path is required")
		}

		cache := cpebble.NewCache(128 * 1024 * 1024)

		db, err := cpebble.Open(targetPath, &cpebble.Options{
			Cache: cache,
		})

		cache.Unref()

		if err != nil {
			return fmt.Errorf("open target pebble %q: %w", targetPath, err)
		}

		defer db.Close()

		copier.targetPebble = db
	}

	logger.WithFields(logrus.Fields{
		"source":     sourceEngine,
		"target":     targetEngine,
		"threads":    threads,
		"copyBlocks": copier.copyBlocks,
		"copyExec":   copier.copyExec,
	}).Info("starting blockdb copy")

	ctx := context.Background()
	if err := copier.run(ctx); err != nil {
		return err
	}

	copier.printFinalStats()

	return nil
}

// blockdbCopier manages the copy operation between blockdb backends.
type blockdbCopier struct {
	logger       logrus.FieldLogger
	threads      int
	copyBlocks   bool
	copyExec     bool
	minSlot      int64 // -1 = no limit
	maxSlot      int64 // -1 = no limit
	sourceEngine string
	targetEngine string

	// S3 source connection.
	sourceS3       *minio.Client
	sourceS3Bucket string
	sourceS3Prefix string

	// Pebble source database.
	sourcePebble *cpebble.DB

	// S3 target connection.
	targetS3       *minio.Client
	targetS3Bucket string
	targetS3Prefix string

	// Pebble target database.
	targetPebble *cpebble.DB

	// Copy statistics.
	slotsOutOfRange atomic.Int64
	objectsScanned  atomic.Int64
	objectsCopied   atomic.Int64
	objectsSkipped  atomic.Int64
	bytesCopied     atomic.Int64
	errors          atomic.Int64
	startTime       time.Time
}

// slotInRange returns true if the given slot falls within the configured
// min/max slot range. A limit of -1 means no limit on that side.
func (c *blockdbCopier) slotInRange(slot uint64) bool {
	if c.minSlot >= 0 && slot < uint64(c.minSlot) {
		return false
	}

	if c.maxSlot >= 0 && slot > uint64(c.maxSlot) {
		return false
	}

	return true
}

// copyWorkItem represents a single piece of work for a copy worker.
type copyWorkItem struct {
	// sourceS3Key is set when the worker must download from S3.
	sourceS3Key string

	// targetS3Key is set when the worker must upload to S3.
	targetS3Key string

	// targetPebbleKey is set when the worker must write a single entry to Pebble.
	targetPebbleKey []byte

	// data holds pre-read data (used for Pebble source or pre-built S3 objects).
	data []byte

	// s3BlockToPebble signals that the worker must parse an S3 block object
	// and write multiple Pebble component entries (cross-backend block copy).
	s3BlockToPebble bool
}

func (c *blockdbCopier) run(ctx context.Context) error {
	c.startTime = time.Now()

	progressCtx, progressCancel := context.WithCancel(ctx)
	defer progressCancel()

	go c.reportProgress(progressCtx)

	workCh := make(chan copyWorkItem, c.threads*2)

	// Start workers.
	var wg sync.WaitGroup

	for i := 0; i < c.threads; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for item := range workCh {
				if err := c.processItem(ctx, item); err != nil {
					c.errors.Add(1)
					c.logger.WithError(err).Error("copy failed")
				}
			}
		}()
	}

	// Enumerate source entries.
	var enumErr error

	switch c.sourceEngine {
	case "s3":
		enumErr = c.enumerateS3(ctx, workCh)
	case "pebble":
		enumErr = c.enumeratePebble(ctx, workCh)
	}

	close(workCh)
	wg.Wait()

	return enumErr
}

// enumerateS3 lists all objects from the source S3 bucket and pushes
// work items for each object that matches the copy filters.
func (c *blockdbCopier) enumerateS3(ctx context.Context, workCh chan<- copyWorkItem) error {
	prefix := c.sourceS3Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	objectsCh := c.sourceS3.ListObjects(ctx, c.sourceS3Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for obj := range objectsCh {
		if obj.Err != nil {
			return fmt.Errorf("list S3 objects: %w", obj.Err)
		}

		c.objectsScanned.Add(1)

		isExec := strings.HasSuffix(obj.Key, "_exec")

		if isExec && !c.copyExec {
			c.objectsSkipped.Add(1)
			continue
		}

		if !isExec && !c.copyBlocks {
			c.objectsSkipped.Add(1)
			continue
		}

		// Filter by slot range if configured.
		if c.minSlot >= 0 || c.maxSlot >= 0 {
			slot := copyCmdParseSlotFromS3Key(obj.Key)
			if !c.slotInRange(slot) {
				c.slotsOutOfRange.Add(1)
				continue
			}
		}

		item := copyWorkItem{sourceS3Key: obj.Key}

		switch {
		case c.targetEngine == "s3":
			// Same-backend: raw object copy with prefix remap.
			item.targetS3Key = copyCmdRemapS3Key(obj.Key, c.sourceS3Prefix, c.targetS3Prefix)

		case isExec:
			// Cross-backend exec data: parse key to build pebble key.
			slot, rootPrefix, err := copyCmdParseS3ExecKey(obj.Key)
			if err != nil {
				c.errors.Add(1)
				c.logger.WithError(err).WithField("key", obj.Key).Warn("skipping unparsable S3 key")

				continue
			}

			item.targetPebbleKey = copyCmdBuildPebbleExecKey(slot, rootPrefix)

		default:
			// Cross-backend block data: worker will parse S3 format,
			// compute root via HashTreeRoot, and write pebble entries.
			item.s3BlockToPebble = true
		}

		c.logger.WithField("key", obj.Key).Debug("enqueued")

		select {
		case workCh <- item:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// enumeratePebble iterates all entries from the source Pebble database
// and pushes work items for entries that match the copy filters.
func (c *blockdbCopier) enumeratePebble(ctx context.Context, workCh chan<- copyWorkItem) error {
	iter, err := c.sourcePebble.NewIter(&cpebble.IterOptions{})
	if err != nil {
		return fmt.Errorf("create pebble iterator: %w", err)
	}
	defer iter.Close()

	// For pebble->pebble slot filtering on block data: cache the slot decision
	// per root since the slot is only in the header component.
	var pebbleSlotFilterRoot []byte
	pebbleSlotFilterPass := true

	// For pebble->S3 block grouping: collect all components for the same root
	// before building a single S3 object. Pebble sorts namespace-1 keys by
	// [ns:2][root:32][type:2], so components for the same root are adjacent.
	var pendingRoot []byte

	pendingComponents := make(map[uint16]*pebbleBlockComponent, 4)

	// flushBlock builds an S3 block object from collected components.
	flushBlock := func() error {
		if len(pendingComponents) == 0 || pendingRoot == nil {
			return nil
		}

		c.objectsScanned.Add(1)

		hdr := pendingComponents[pebbleBlockTypeHeader]
		if hdr == nil || len(hdr.data) == 0 {
			c.logger.WithField("root", hex.EncodeToString(pendingRoot[:4])).Warn("block has no header, skipping")
			c.objectsSkipped.Add(1)

			return nil
		}

		slot, err := copyCmdExtractSlotFromHeader(hdr.data)
		if err != nil {
			c.logger.WithError(err).WithField("root", hex.EncodeToString(pendingRoot[:4])).Warn("cannot parse header, skipping")
			c.errors.Add(1)

			return nil
		}

		if !c.slotInRange(slot) {
			c.slotsOutOfRange.Add(1)
			return nil
		}

		s3Data := copyCmdBuildS3BlockObject(pendingRoot, pendingComponents)
		s3Key := copyCmdBuildS3BlockKey(c.targetS3Prefix, slot, pendingRoot)

		select {
		case workCh <- copyWorkItem{targetS3Key: s3Key, data: s3Data}:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	}

	for iter.First(); iter.Valid(); iter.Next() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		key := iter.Key()
		if len(key) < 2 {
			continue
		}

		ns := binary.BigEndian.Uint16(key[:2])

		// Classify entry type.
		isExecData := ns == pebbleNsExecData && len(key) == 14
		isBlockComponent := ns == pebbleNsBlock && len(key) >= 36

		if c.targetEngine == "pebble" {
			// Same-backend: copy raw key/value, filter by type.
			if isExecData && !c.copyExec {
				c.objectsSkipped.Add(1)
				continue
			}

			if !isExecData && !c.copyBlocks {
				c.objectsSkipped.Add(1)
				continue
			}

			// Slot range filtering for pebble->pebble.
			if c.minSlot >= 0 || c.maxSlot >= 0 {
				if isExecData {
					slot := binary.BigEndian.Uint64(key[2:10])
					if !c.slotInRange(slot) {
						c.slotsOutOfRange.Add(1)
						continue
					}
				} else if isBlockComponent {
					root := key[2 : len(key)-2]
					blockType := binary.BigEndian.Uint16(key[len(key)-2:])

					// Track slot decision per root using the header component.
					if !bytes.Equal(root, pebbleSlotFilterRoot) {
						pebbleSlotFilterRoot = make([]byte, len(root))
						copy(pebbleSlotFilterRoot, root)
						pebbleSlotFilterPass = true // default pass if no header

						if blockType == pebbleBlockTypeHeader {
							value := iter.Value()
							if len(value) > pebbleValueHeaderSize {
								slot, err := copyCmdExtractSlotFromHeader(value[pebbleValueHeaderSize:])
								if err == nil {
									pebbleSlotFilterPass = c.slotInRange(slot)
								}
							}
						}
					}

					if !pebbleSlotFilterPass {
						c.slotsOutOfRange.Add(1)
						continue
					}
				}
			}

			c.objectsScanned.Add(1)

			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)

			value := iter.Value()
			valueCopy := make([]byte, len(value))
			copy(valueCopy, value)

			select {
			case workCh <- copyWorkItem{targetPebbleKey: keyCopy, data: valueCopy}:
			case <-ctx.Done():
				return ctx.Err()
			}

			continue
		}

		// Cross-backend: pebble -> S3.

		// Handle block components: group by root, then flush as S3 object.
		if isBlockComponent && c.copyBlocks {
			root := key[2 : len(key)-2]
			blockType := binary.BigEndian.Uint16(key[len(key)-2:])

			// Flush previous root if we moved to a new one.
			if pendingRoot != nil && !bytes.Equal(root, pendingRoot) {
				if err := flushBlock(); err != nil {
					return err
				}

				pendingComponents = make(map[uint16]*pebbleBlockComponent, 4)
			}

			if pendingRoot == nil || !bytes.Equal(root, pendingRoot) {
				pendingRoot = make([]byte, len(root))
				copy(pendingRoot, root)
			}

			value := iter.Value()
			ver, data := copyCmdParsePebbleBlockValue(value)
			dataCopy := make([]byte, len(data))
			copy(dataCopy, data)

			pendingComponents[blockType] = &pebbleBlockComponent{version: ver, data: dataCopy}

			continue
		}

		// Flush any pending block when leaving namespace 1.
		if pendingRoot != nil {
			if err := flushBlock(); err != nil {
				return err
			}

			pendingRoot = nil
			pendingComponents = make(map[uint16]*pebbleBlockComponent, 4)
		}

		// Handle exec data.
		if isExecData && c.copyExec {
			// Slot range filtering for exec data (slot is in pebble key).
			if c.minSlot >= 0 || c.maxSlot >= 0 {
				slot := binary.BigEndian.Uint64(key[2:10])
				if !c.slotInRange(slot) {
					c.slotsOutOfRange.Add(1)
					continue
				}
			}

			c.objectsScanned.Add(1)

			keyCopy := make([]byte, len(key))
			copy(keyCopy, key)

			value := iter.Value()
			valueCopy := make([]byte, len(value))
			copy(valueCopy, value)

			slot, hashPrefix := copyCmdParsePebbleExecKey(keyCopy)
			targetKey := copyCmdBuildS3ExecKey(c.targetS3Prefix, slot, hashPrefix)

			select {
			case workCh <- copyWorkItem{targetS3Key: targetKey, data: valueCopy}:
			case <-ctx.Done():
				return ctx.Err()
			}

			continue
		}

		c.objectsSkipped.Add(1)
	}

	// Flush final pending block.
	if pendingRoot != nil {
		if err := flushBlock(); err != nil {
			return err
		}
	}

	if err := iter.Error(); err != nil {
		return fmt.Errorf("pebble iteration: %w", err)
	}

	return nil
}

// processItem handles a single copy work item: reads data from source
// (if not pre-read) and writes to the target backend.
func (c *blockdbCopier) processItem(ctx context.Context, item copyWorkItem) error {
	data := item.data

	// Download from S3 source if data was not pre-read.
	if data == nil && item.sourceS3Key != "" {
		obj, err := c.sourceS3.GetObject(ctx, c.sourceS3Bucket, item.sourceS3Key, minio.GetObjectOptions{})
		if err != nil {
			return fmt.Errorf("get s3://%s/%s: %w", c.sourceS3Bucket, item.sourceS3Key, err)
		}

		var readErr error

		data, readErr = io.ReadAll(obj)
		obj.Close()

		if readErr != nil {
			return fmt.Errorf("read s3://%s/%s: %w", c.sourceS3Bucket, item.sourceS3Key, readErr)
		}
	}

	// Cross-backend: S3 block -> Pebble components.
	if item.s3BlockToPebble {
		return c.processS3BlockToPebble(data)
	}

	// Write to S3 target.
	if item.targetS3Key != "" {
		_, err := c.targetS3.PutObject(
			ctx, c.targetS3Bucket, item.targetS3Key,
			bytes.NewReader(data), int64(len(data)),
			minio.PutObjectOptions{ContentType: "application/octet-stream"},
		)
		if err != nil {
			return fmt.Errorf("put s3://%s/%s: %w", c.targetS3Bucket, item.targetS3Key, err)
		}
	}

	// Write to Pebble target.
	if item.targetPebbleKey != nil {
		if err := c.targetPebble.Set(item.targetPebbleKey, data, nil); err != nil {
			return fmt.Errorf("pebble set: %w", err)
		}
	}

	c.objectsCopied.Add(1)
	c.bytesCopied.Add(int64(len(data)))

	return nil
}

// processS3BlockToPebble parses an S3 block object, computes the block root
// from the SSZ header via HashTreeRoot, and writes each component to Pebble.
func (c *blockdbCopier) processS3BlockToPebble(s3Data []byte) error {
	meta, err := copyCmdParseS3BlockMeta(s3Data)
	if err != nil {
		return fmt.Errorf("parse S3 block metadata: %w", err)
	}

	metaSize := meta.metaSize()

	// Extract header data and compute block root.
	if meta.headerLen == 0 {
		return fmt.Errorf("S3 block object has no header data")
	}

	headerEnd := metaSize + meta.headerLen
	if headerEnd > uint32(len(s3Data)) {
		return fmt.Errorf("S3 block object truncated (header)")
	}

	headerData := s3Data[metaSize:headerEnd]

	root, err := copyCmdComputeBlockRoot(headerData)
	if err != nil {
		return fmt.Errorf("compute block root: %w", err)
	}

	var totalBytes int64

	// Write header to pebble.
	pebbleKey := copyCmdBuildPebbleBlockKey(root[:], pebbleBlockTypeHeader)
	pebbleVal := copyCmdBuildPebbleBlockValue(uint64(meta.objVersion), headerData)

	if err := c.targetPebble.Set(pebbleKey, pebbleVal, nil); err != nil {
		return fmt.Errorf("pebble set header: %w", err)
	}

	totalBytes += int64(len(pebbleVal))

	// Write body.
	if meta.bodyLen > 0 {
		bodyStart := headerEnd
		bodyEnd := bodyStart + meta.bodyLen

		if bodyEnd > uint32(len(s3Data)) {
			return fmt.Errorf("S3 block object truncated (body)")
		}

		bodyData := s3Data[bodyStart:bodyEnd]
		pebbleKey = copyCmdBuildPebbleBlockKey(root[:], pebbleBlockTypeBody)
		pebbleVal = copyCmdBuildPebbleBlockValue(uint64(meta.bodyVersion), bodyData)

		if err := c.targetPebble.Set(pebbleKey, pebbleVal, nil); err != nil {
			return fmt.Errorf("pebble set body: %w", err)
		}

		totalBytes += int64(len(pebbleVal))
	}

	// Write payload (v2+).
	if meta.objVersion >= 2 && meta.payloadLen > 0 {
		payStart := headerEnd + meta.bodyLen
		payEnd := payStart + meta.payloadLen

		if payEnd > uint32(len(s3Data)) {
			return fmt.Errorf("S3 block object truncated (payload)")
		}

		payData := s3Data[payStart:payEnd]
		pebbleKey = copyCmdBuildPebbleBlockKey(root[:], pebbleBlockTypePayload)
		pebbleVal = copyCmdBuildPebbleBlockValue(uint64(meta.payloadVersion), payData)

		if err := c.targetPebble.Set(pebbleKey, pebbleVal, nil); err != nil {
			return fmt.Errorf("pebble set payload: %w", err)
		}

		totalBytes += int64(len(pebbleVal))
	}

	// Write BAL (v2+).
	if meta.objVersion >= 2 && meta.balLen > 0 {
		balStart := headerEnd + meta.bodyLen + meta.payloadLen
		balEnd := balStart + meta.balLen

		if balEnd > uint32(len(s3Data)) {
			return fmt.Errorf("S3 block object truncated (bal)")
		}

		balData := s3Data[balStart:balEnd]
		pebbleKey = copyCmdBuildPebbleBlockKey(root[:], pebbleBlockTypeBal)
		pebbleVal = copyCmdBuildPebbleBlockValue(uint64(meta.balVersion), balData)

		if err := c.targetPebble.Set(pebbleKey, pebbleVal, nil); err != nil {
			return fmt.Errorf("pebble set bal: %w", err)
		}

		totalBytes += int64(len(pebbleVal))
	}

	c.objectsCopied.Add(1)
	c.bytesCopied.Add(totalBytes)

	return nil
}

// reportProgress periodically logs copy progress until the context is cancelled.
func (c *blockdbCopier) reportProgress(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(c.startTime)
			copied := c.objectsCopied.Load()

			rate := float64(0)
			if elapsed.Seconds() > 0 {
				rate = float64(copied) / elapsed.Seconds()
			}

			fields := logrus.Fields{
				"scanned": c.objectsScanned.Load(),
				"copied":  copied,
				"skipped": c.objectsSkipped.Load(),
				"errors":  c.errors.Load(),
				"bytes":   formatBytes(c.bytesCopied.Load()),
				"rate":    fmt.Sprintf("%.1f/s", rate),
				"elapsed": elapsed.Truncate(time.Second),
			}
			if c.minSlot >= 0 || c.maxSlot >= 0 {
				fields["outOfRange"] = c.slotsOutOfRange.Load()
			}
			c.logger.WithFields(fields).Info("copy progress")
		}
	}
}

// printFinalStats logs the final copy statistics.
func (c *blockdbCopier) printFinalStats() {
	elapsed := time.Since(c.startTime)
	copied := c.objectsCopied.Load()

	rate := float64(0)
	if elapsed.Seconds() > 0 {
		rate = float64(copied) / elapsed.Seconds()
	}

	fields := logrus.Fields{
		"scanned": c.objectsScanned.Load(),
		"copied":  copied,
		"skipped": c.objectsSkipped.Load(),
		"errors":  c.errors.Load(),
		"bytes":   formatBytes(c.bytesCopied.Load()),
		"rate":    fmt.Sprintf("%.1f/s", rate),
		"elapsed": elapsed.Truncate(time.Second),
	}
	if c.minSlot >= 0 || c.maxSlot >= 0 {
		fields["outOfRange"] = c.slotsOutOfRange.Load()
	}
	c.logger.WithFields(fields).Info("copy complete")
}

// --- S3 client helper ---

// newCopyS3Client creates a minio S3 client from command flags with the given
// prefix ("source" or "target").
func newCopyS3Client(cmd *cobra.Command, prefix string) (*minio.Client, string, string, error) {
	endpoint, _ := cmd.Flags().GetString(prefix + "-s3-endpoint")
	bucket, _ := cmd.Flags().GetString(prefix + "-s3-bucket")
	accessKey, _ := cmd.Flags().GetString(prefix + "-s3-access-key")
	secretKey, _ := cmd.Flags().GetString(prefix + "-s3-secret-key")
	region, _ := cmd.Flags().GetString(prefix + "-s3-region")
	s3Path, _ := cmd.Flags().GetString(prefix + "-s3-path")
	secure, _ := cmd.Flags().GetBool(prefix + "-s3-secure")

	if endpoint == "" {
		return nil, "", "", fmt.Errorf("--%s-s3-endpoint is required", prefix)
	}

	if bucket == "" {
		return nil, "", "", fmt.Errorf("--%s-s3-bucket is required", prefix)
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("create client: %w", err)
	}

	exists, err := client.BucketExists(context.Background(), bucket)
	if err != nil {
		return nil, "", "", fmt.Errorf("check bucket %q: %w", bucket, err)
	}

	if !exists {
		return nil, "", "", fmt.Errorf("bucket %q does not exist", bucket)
	}

	pathPrefix := strings.TrimPrefix(s3Path, "/")

	return client, bucket, pathPrefix, nil
}

// --- S3 key helpers ---

// copyCmdRemapS3Key maps a source S3 object key to the target path prefix.
func copyCmdRemapS3Key(key, sourcePrefix, targetPrefix string) string {
	rel := key
	if sourcePrefix != "" {
		rel = strings.TrimPrefix(key, sourcePrefix)
		rel = strings.TrimPrefix(rel, "/")
	}

	if targetPrefix == "" {
		return rel
	}

	return targetPrefix + "/" + rel
}

// copyCmdParseSlotFromS3Key extracts the slot number from an S3 object key.
// Works for both block and exec data keys.
// Format: .../{slot:010d}_{rootHex}[_exec]
func copyCmdParseSlotFromS3Key(key string) uint64 {
	filename := key
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		filename = key[idx+1:]
	}

	sep := strings.Index(filename, "_")
	if sep <= 0 {
		return 0
	}

	var slot uint64
	if _, err := fmt.Sscanf(filename[:sep], "%d", &slot); err != nil {
		return 0
	}

	return slot
}

// copyCmdParseS3ExecKey extracts slot and root prefix from an S3 exec data key.
// Key format: .../{slot:010d}_{rootHex}_exec
func copyCmdParseS3ExecKey(key string) (uint64, []byte, error) {
	filename := key
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		filename = key[idx+1:]
	}

	if !strings.HasSuffix(filename, "_exec") {
		return 0, nil, fmt.Errorf("not an exec data key")
	}

	filename = strings.TrimSuffix(filename, "_exec")

	sep := strings.Index(filename, "_")
	if sep <= 0 {
		return 0, nil, fmt.Errorf("invalid filename format: %s", filename)
	}

	var slot uint64
	if _, err := fmt.Sscanf(filename[:sep], "%d", &slot); err != nil {
		return 0, nil, fmt.Errorf("parse slot from %q: %w", filename[:sep], err)
	}

	rootPrefix, err := hex.DecodeString(filename[sep+1:])
	if err != nil {
		return 0, nil, fmt.Errorf("decode root hex %q: %w", filename[sep+1:], err)
	}

	return slot, rootPrefix, nil
}

// copyCmdBuildS3BlockKey constructs an S3 block object key.
// Format: {prefix}/{tier:06d}/{slot:010d}_{rootHex}
func copyCmdBuildS3BlockKey(prefix string, slot uint64, root []byte) string {
	rootHex := hex.EncodeToString(root[:4])
	tier := fmt.Sprintf("%06d", slot/10000)
	filename := fmt.Sprintf("%010d_%s", slot, rootHex)

	return path.Join(prefix, tier, filename)
}

// copyCmdBuildS3ExecKey constructs an S3 exec data object key.
// Format: {prefix}/{tier:06d}/{slot:010d}_{rootHex}_exec
func copyCmdBuildS3ExecKey(prefix string, slot uint64, hashPrefix []byte) string {
	rootHex := hex.EncodeToString(hashPrefix)
	tier := fmt.Sprintf("%06d", slot/10000)
	filename := fmt.Sprintf("%010d_%s_exec", slot, rootHex)

	return path.Join(prefix, tier, filename)
}

// --- Pebble key/value helpers ---

// copyCmdBuildPebbleBlockKey constructs a Pebble block component key.
// Format: [namespace:2][root:32][type:2]
func copyCmdBuildPebbleBlockKey(root []byte, blockType uint16) []byte {
	key := make([]byte, 2+len(root)+2)
	binary.BigEndian.PutUint16(key[:2], pebbleNsBlock)
	copy(key[2:], root)
	binary.BigEndian.PutUint16(key[2+len(root):], blockType)

	return key
}

// copyCmdBuildPebbleBlockValue constructs a Pebble block component value.
// Format: [version:8][timestamp:8][data...]
func copyCmdBuildPebbleBlockValue(version uint64, data []byte) []byte {
	value := make([]byte, pebbleValueHeaderSize+len(data))
	binary.BigEndian.PutUint64(value[:8], version)
	binary.BigEndian.PutUint64(value[8:16], uint64(time.Now().UnixNano()))
	copy(value[pebbleValueHeaderSize:], data)

	return value
}

// copyCmdParsePebbleBlockValue extracts version and data from a Pebble value.
func copyCmdParsePebbleBlockValue(value []byte) (uint64, []byte) {
	if len(value) < pebbleValueHeaderSize {
		return 0, nil
	}

	version := binary.BigEndian.Uint64(value[:8])

	data := make([]byte, len(value)-pebbleValueHeaderSize)
	copy(data, value[pebbleValueHeaderSize:])

	return version, data
}

// copyCmdBuildPebbleExecKey constructs a Pebble exec data key.
// Format: [namespace:2][slot:8][hashPrefix:4]
func copyCmdBuildPebbleExecKey(slot uint64, hashPrefix []byte) []byte {
	key := make([]byte, 14)
	binary.BigEndian.PutUint16(key[0:2], pebbleNsExecData)
	binary.BigEndian.PutUint64(key[2:10], slot)

	n := min(len(hashPrefix), 4)
	copy(key[10:10+n], hashPrefix[:n])

	return key
}

// copyCmdParsePebbleExecKey extracts slot and hash prefix from a Pebble exec data key.
func copyCmdParsePebbleExecKey(key []byte) (uint64, []byte) {
	slot := binary.BigEndian.Uint64(key[2:10])

	hashPrefix := make([]byte, 4)
	copy(hashPrefix, key[10:14])

	return slot, hashPrefix
}

// --- S3 block object format helpers ---

// pebbleBlockComponent holds a single block component extracted from Pebble.
type pebbleBlockComponent struct {
	version uint64
	data    []byte
}

// s3BlockMeta holds parsed S3 block object metadata.
type s3BlockMeta struct {
	objVersion     uint32
	headerLen      uint32
	bodyVersion    uint32
	bodyLen        uint32
	payloadVersion uint32
	payloadLen     uint32
	balVersion     uint32
	balLen         uint32
}

func (m *s3BlockMeta) metaSize() uint32 {
	if m.objVersion >= 2 {
		return s3MetaSizeV2
	}

	return s3MetaSizeV1
}

// copyCmdParseS3BlockMeta reads metadata from an S3 block object.
func copyCmdParseS3BlockMeta(data []byte) (*s3BlockMeta, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("data too short for version field")
	}

	version := binary.BigEndian.Uint32(data[:4])
	meta := &s3BlockMeta{objVersion: version}

	switch version {
	case 1:
		if len(data) < s3MetaSizeV1 {
			return nil, fmt.Errorf("data too short for v1 metadata: need %d, got %d", s3MetaSizeV1, len(data))
		}

		meta.headerLen = binary.BigEndian.Uint32(data[4:8])
		meta.bodyVersion = binary.BigEndian.Uint32(data[8:12])
		meta.bodyLen = binary.BigEndian.Uint32(data[12:16])

	case 2:
		if len(data) < s3MetaSizeV2 {
			return nil, fmt.Errorf("data too short for v2 metadata: need %d, got %d", s3MetaSizeV2, len(data))
		}

		meta.headerLen = binary.BigEndian.Uint32(data[4:8])
		meta.bodyVersion = binary.BigEndian.Uint32(data[8:12])
		meta.bodyLen = binary.BigEndian.Uint32(data[12:16])
		meta.payloadVersion = binary.BigEndian.Uint32(data[16:20])
		meta.payloadLen = binary.BigEndian.Uint32(data[20:24])
		meta.balVersion = binary.BigEndian.Uint32(data[24:28])
		meta.balLen = binary.BigEndian.Uint32(data[28:32])

	default:
		return nil, fmt.Errorf("unsupported S3 object version: %d", version)
	}

	return meta, nil
}

// copyCmdBuildS3BlockObject assembles an S3 block object from pebble components.
// The components map uses pebble block type constants as keys.
func copyCmdBuildS3BlockObject(root []byte, components map[uint16]*pebbleBlockComponent) []byte {
	hdr := components[pebbleBlockTypeHeader]
	body := components[pebbleBlockTypeBody]
	payload := components[pebbleBlockTypePayload]
	bal := components[pebbleBlockTypeBal]

	var headerData, bodyData, payloadData, balData []byte

	var bodyVersion uint64

	if hdr != nil {
		headerData = hdr.data
	}

	if body != nil {
		bodyVersion = body.version
		bodyData = body.data
	}

	if payload != nil {
		payloadData = payload.data
	}

	if bal != nil {
		balData = bal.data
	}

	// Use v2 format for Gloas+ blocks.
	useV2 := bodyVersion >= uint64(spec.DataVersionGloas)

	if useV2 {
		meta := make([]byte, s3MetaSizeV2)
		binary.BigEndian.PutUint32(meta[0:4], 2)
		binary.BigEndian.PutUint32(meta[4:8], uint32(len(headerData)))
		binary.BigEndian.PutUint32(meta[8:12], uint32(bodyVersion))
		binary.BigEndian.PutUint32(meta[12:16], uint32(len(bodyData)))

		payVer := uint32(0)
		if payload != nil {
			payVer = uint32(payload.version)
		}

		balVer := uint32(0)
		if bal != nil {
			balVer = uint32(bal.version)
		}

		binary.BigEndian.PutUint32(meta[16:20], payVer)
		binary.BigEndian.PutUint32(meta[20:24], uint32(len(payloadData)))
		binary.BigEndian.PutUint32(meta[24:28], balVer)
		binary.BigEndian.PutUint32(meta[28:32], uint32(len(balData)))

		buf := make([]byte, 0, len(meta)+len(headerData)+len(bodyData)+len(payloadData)+len(balData))
		buf = append(buf, meta...)
		buf = append(buf, headerData...)
		buf = append(buf, bodyData...)
		buf = append(buf, payloadData...)
		buf = append(buf, balData...)

		return buf
	}

	meta := make([]byte, s3MetaSizeV1)
	binary.BigEndian.PutUint32(meta[0:4], 1)
	binary.BigEndian.PutUint32(meta[4:8], uint32(len(headerData)))
	binary.BigEndian.PutUint32(meta[8:12], uint32(bodyVersion))
	binary.BigEndian.PutUint32(meta[12:16], uint32(len(bodyData)))

	buf := make([]byte, 0, len(meta)+len(headerData)+len(bodyData))
	buf = append(buf, meta...)
	buf = append(buf, headerData...)
	buf = append(buf, bodyData...)

	return buf
}

// --- SSZ header helpers ---

// copyCmdComputeBlockRoot unmarshals the SSZ header and computes the block root.
func copyCmdComputeBlockRoot(headerData []byte) (phase0.Root, error) {
	header := &phase0.SignedBeaconBlockHeader{}
	if err := header.UnmarshalSSZ(headerData); err != nil {
		return phase0.Root{}, fmt.Errorf("unmarshal SignedBeaconBlockHeader: %w", err)
	}

	root, err := header.Message.HashTreeRoot()
	if err != nil {
		return phase0.Root{}, fmt.Errorf("HashTreeRoot: %w", err)
	}

	return root, nil
}

// copyCmdExtractSlotFromHeader unmarshals the SSZ header and returns the slot.
func copyCmdExtractSlotFromHeader(headerData []byte) (uint64, error) {
	header := &phase0.SignedBeaconBlockHeader{}
	if err := header.UnmarshalSSZ(headerData); err != nil {
		return 0, fmt.Errorf("unmarshal SignedBeaconBlockHeader: %w", err)
	}

	return uint64(header.Message.Slot), nil
}
