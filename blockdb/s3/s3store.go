package s3

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/ethpandaops/dora/blockdb/types"
	dtypes "github.com/ethpandaops/dora/types"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Engine struct {
	client     *minio.Client
	bucket     string
	pathPrefix string
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

	engine := &S3Engine{
		client:     client,
		bucket:     config.Bucket,
		pathPrefix: strings.TrimPrefix(config.Path, "/"),
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

type objectMetadata struct {
	objVersion   uint32
	headerLength uint32
	bodyVersion  uint32
	bodyLength   uint32
}

func (e *S3Engine) readObjectMetadata(data []byte) (*objectMetadata, int, error) {
	metadataLength := 4
	metadata := &objectMetadata{
		objVersion: binary.BigEndian.Uint32(data[:4]),
	}

	switch metadata.objVersion {
	case 1:
		metadata.headerLength = binary.BigEndian.Uint32(data[4:8])
		metadata.bodyVersion = binary.BigEndian.Uint32(data[8:12])
		metadata.bodyLength = binary.BigEndian.Uint32(data[12:16])
		metadataLength += 12
	}

	return metadata, metadataLength, nil
}

func (e *S3Engine) writeObjectMetadata(metadata *objectMetadata) []byte {
	data := make([]byte, 4, 16)
	binary.BigEndian.PutUint32(data, metadata.objVersion)

	switch metadata.objVersion {
	case 1:
		data = binary.BigEndian.AppendUint32(data, metadata.headerLength)
		data = binary.BigEndian.AppendUint32(data, metadata.bodyVersion)
		data = binary.BigEndian.AppendUint32(data, metadata.bodyLength)
	}

	return data
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
	buf := make([]byte, 1024)
	buflen, err := obj.Read(buf)
	if (err != nil && err != io.EOF) || buflen == 0 {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	metadata, metadataLength, err := e.readObjectMetadata(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	headerData := make([]byte, metadata.headerLength)
	headerOffset := 0
	if buflen > metadataLength {
		copy(headerData, buf[metadataLength:buflen])
		headerOffset = buflen - metadataLength
	}

	if buflen < int(metadataLength)+int(metadata.headerLength) {
		_, err = obj.Read(headerData[headerOffset:])
		if err != nil {
			return nil, fmt.Errorf("failed to read header data: %w", err)
		}
	}

	bodyData := make([]byte, metadata.bodyLength)
	bodyOffset := 0
	if buflen > int(metadataLength)+int(metadata.headerLength) {
		copy(bodyData, buf[int(metadataLength)+int(metadata.headerLength):buflen])
		bodyOffset = buflen - int(metadataLength) - int(metadata.headerLength)
	}

	if buflen < int(metadataLength)+int(metadata.headerLength)+int(metadata.bodyLength) {
		_, err = obj.Read(bodyData[bodyOffset:])
		if err != nil {
			return nil, fmt.Errorf("failed to read body data: %w", err)
		}
	}

	blockData := &types.BlockData{
		HeaderVersion: uint64(metadata.objVersion),
		HeaderData:    headerData,
		BodyVersion:   uint64(metadata.bodyVersion),
	}

	if parseBlock != nil {
		body, err := parseBlock(uint64(metadata.bodyVersion), bodyData)
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

	metadata := &objectMetadata{
		objVersion:   uint32(blockData.HeaderVersion),
		headerLength: uint32(len(blockData.HeaderData)),
		bodyVersion:  uint32(blockData.BodyVersion),
		bodyLength:   uint32(len(blockData.BodyData)),
	}

	metadataBytes := e.writeObjectMetadata(metadata)
	metadataLength := len(metadataBytes)

	// Prepare data with header and body versions and lengths
	data := make([]byte, metadataLength+int(metadata.headerLength)+int(metadata.bodyLength))
	copy(data[:metadataLength], metadataBytes)
	copy(data[metadataLength:metadataLength+int(metadata.headerLength)], blockData.HeaderData)
	copy(data[metadataLength+int(metadata.headerLength):], blockData.BodyData)

	// Upload object
	_, err = e.client.PutObject(
		ctx,
		e.bucket,
		key,
		bytes.NewReader(data),
		int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	if err != nil {
		return false, fmt.Errorf("failed to upload block: %w", err)
	}

	return true, nil
}
