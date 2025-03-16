package s3

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
)

type uploadTask struct {
	key       string
	data      []byte
	resultCh  chan error
	cancelled bool
}

type S3Engine struct {
	client     *minio.Client
	bucket     string
	pathPrefix string

	uploadQueue    chan *uploadTask
	uploadersDone  sync.WaitGroup
	uploadersCtx   context.Context
	uploadersClose context.CancelFunc
}

func NewS3Engine(config dtypes.S3BlockDBConfig) (types.BlockDbEngine, error) {
	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.Secure,
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

	if config.UploadQueueSize == 0 {
		config.UploadQueueSize = 100
	}
	if config.MaxConcurrentUploads == 0 {
		config.MaxConcurrentUploads = 10
	}

	uploaderCtx, uploaderClose := context.WithCancel(context.Background())
	engine := &S3Engine{
		client:         client,
		bucket:         config.Bucket,
		pathPrefix:     strings.TrimPrefix(config.Path, "/"),
		uploadQueue:    make(chan *uploadTask, config.UploadQueueSize),
		uploadersCtx:   uploaderCtx,
		uploadersClose: uploaderClose,
	}

	// Start upload workers
	engine.uploadersDone.Add(int(config.MaxConcurrentUploads))
	for i := uint(0); i < config.MaxConcurrentUploads; i++ {
		go engine.uploadWorker()
	}

	return engine, nil
}

func (e *S3Engine) uploadWorker() {
	defer e.uploadersDone.Done()

	for {
		select {
		case <-e.uploadersCtx.Done():
			return
		case task := <-e.uploadQueue:
			if task == nil || task.cancelled {
				continue
			}

			var err error
			for retry := 0; retry < 3; retry++ {
				_, err = e.client.PutObject(
					e.uploadersCtx,
					e.bucket,
					task.key,
					bytes.NewReader(task.data),
					int64(len(task.data)),
					minio.PutObjectOptions{ContentType: "application/octet-stream"},
				)

				if err == nil || retry >= 3 {
					break
				}
			}

			if err != nil {
				logrus.Errorf("Failed to upload block %s: %v", task.key, err)
			}

			if task.resultCh != nil {
				task.resultCh <- err
				close(task.resultCh)
			}
		}
	}
}

func (e *S3Engine) Close() error {
	e.uploadersClose()     // Signal workers to stop
	e.uploadersDone.Wait() // Wait for all uploads to complete
	close(e.uploadQueue)
	return nil
}

func (e *S3Engine) getObjectKey(root []byte, slot uint64) string {
	rootHex := hex.EncodeToString(root[:4]) // First 4 bytes
	return path.Join(e.pathPrefix, fmt.Sprintf("%06d", slot/10000), fmt.Sprintf("%010d_%s", slot, rootHex))
}

func (e *S3Engine) GetBlockHeader(slot uint64, root []byte) ([]byte, uint64, error) {
	key := e.getObjectKey(root, slot)

	obj, err := e.client.GetObject(context.Background(), e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// Read version (first 8 bytes)
	versionBytes := make([]byte, 8)
	_, err = obj.Read(versionBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read version: %w", err)
	}
	version := binary.BigEndian.Uint64(versionBytes)

	// Read the rest (header data)
	var headerData bytes.Buffer
	_, err = headerData.ReadFrom(obj)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read header data: %w", err)
	}

	return headerData.Bytes(), version, nil
}

func (e *S3Engine) GetBlockBody(slot uint64, root []byte, parser func(uint64, []byte) (interface{}, error)) (interface{}, error) {
	key := e.getObjectKey(root, slot)

	obj, err := e.client.GetObject(context.Background(), e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// Read version
	versionBytes := make([]byte, 8)
	_, err = obj.Read(versionBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	version := binary.BigEndian.Uint64(versionBytes)

	// Read body data
	var bodyData bytes.Buffer
	_, err = bodyData.ReadFrom(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read body data: %w", err)
	}

	return parser(version, bodyData.Bytes())
}

func (e *S3Engine) GetBlock(ctx context.Context, slot uint64, root []byte, parseBlock func(uint64, []byte) (interface{}, error)) (*types.BlockData, error) {
	key := e.getObjectKey(root, slot)

	obj, err := e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	// read metadata
	metadata := make([]byte, 16)
	_, err = obj.Read(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}
	headerVersion := binary.BigEndian.Uint32(metadata[:4])
	headerLength := binary.BigEndian.Uint32(metadata[4:8])
	bodyVersion := binary.BigEndian.Uint32(metadata[8:12])
	bodyLength := binary.BigEndian.Uint32(metadata[12:16])

	headerData := make([]byte, headerLength)
	_, err = obj.Read(headerData)
	if err != nil {
		return nil, fmt.Errorf("failed to read header data: %w", err)
	}

	bodyData := make([]byte, bodyLength)
	_, err = obj.Read(bodyData)
	if err != nil {
		return nil, fmt.Errorf("failed to read body data: %w", err)
	}

	blockData := &types.BlockData{
		HeaderVersion: uint64(headerVersion),
		HeaderData:    headerData,
		BodyVersion:   uint64(bodyVersion),
	}

	if parseBlock != nil {
		body, err := parseBlock(uint64(bodyVersion), bodyData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse body: %w", err)
		}

		blockData.Body = body
	} else {
		blockData.BodyData = bodyData
	}

	return blockData, nil
}

func (e *S3Engine) AddBlock(ctx context.Context, slot uint64, root []byte, dataCb func() (*types.BlockData, error)) (bool, error) {
	key := e.getObjectKey(root, slot)

	// Check if object already exists
	stat, err := e.client.StatObject(ctx, e.bucket, key, minio.StatObjectOptions{})
	if err == nil && stat.Size > 0 {
		return false, nil
	}

	blockData, err := dataCb()
	if err != nil {
		return false, fmt.Errorf("failed to get block data: %w", err)
	}

	// Prepare data with header and body versions and lengths
	data := make([]byte, 16+len(blockData.HeaderData)+len(blockData.BodyData))
	binary.BigEndian.PutUint32(data[:4], uint32(blockData.HeaderVersion))
	binary.BigEndian.PutUint32(data[4:8], uint32(len(blockData.HeaderData)))
	binary.BigEndian.PutUint32(data[8:12], uint32(blockData.BodyVersion))
	binary.BigEndian.PutUint32(data[12:16], uint32(len(blockData.BodyData)))

	copy(data[16:], blockData.HeaderData)
	copy(data[16+len(blockData.HeaderData):], blockData.BodyData)

	// Create upload task
	resultCh := make(chan error, 1)
	task := &uploadTask{
		key:      key,
		data:     data,
		resultCh: resultCh,
	}

	// Add to upload queue
	select {
	case e.uploadQueue <- task:
		// Task queued successfully
	case <-ctx.Done():
		task.cancelled = true
		return false, ctx.Err()
	}

	return true, nil
}
