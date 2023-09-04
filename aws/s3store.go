package aws

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Store struct {
	s3Client  *s3.Client
	awsBucket string
}

func NewS3Store(accessKey string, secretKey string, awsRegion string, awsBucket string) (*S3Store, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithCredentialsProvider(creds), config.WithRegion(awsRegion))
	if err != nil {
		return nil, fmt.Errorf("could not load aws config: %w", err)
	}

	s3Store := &S3Store{
		s3Client:  s3.NewFromConfig(cfg),
		awsBucket: awsBucket,
	}
	return s3Store, nil
}

func (store *S3Store) Upload(key string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	reader := bytes.NewReader(data)
	uploader := manager.NewUploader(store.s3Client)
	_, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(store.awsBucket),
		Key:    aws.String(key),
		Body:   reader,
	})
	if err != nil {
		return fmt.Errorf("could not upload '%v' to s3: %w", key, err)
	}
	return nil
}

func (store *S3Store) Download(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	dataBuf := manager.NewWriteAtBuffer([]byte{})
	downloader := manager.NewDownloader(store.s3Client)
	_, err := downloader.Download(ctx, dataBuf, &s3.GetObjectInput{
		Bucket: aws.String(store.awsBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("could not download '%v' from s3: %w", key, err)
	}
	return dataBuf.Bytes(), nil
}
