package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var s3ClearCmd = &cobra.Command{
	Use:   "s3-clear",
	Short: "Clear all objects under an S3 path",
	Long: `Remove all objects under the configured S3 path prefix.
Used to clean up a dora instance's data from a shared S3 bucket.

Requires a config file with blockDb.s3 settings. The path prefix
from the config determines which objects are deleted.

Example:
  dora-utils s3-clear -c config.yaml
  dora-utils s3-clear -c config.yaml --dry-run`,
	RunE: runS3Clear,
}

func init() {
	rootCmd.AddCommand(s3ClearCmd)

	s3ClearCmd.Flags().StringP("config", "c", "", "Path to the config file (required)")
	s3ClearCmd.Flags().Bool("dry-run", false, "List objects that would be deleted without deleting them")
	s3ClearCmd.Flags().BoolP("verbose", "v", false, "Verbose output")

	if err := s3ClearCmd.MarkFlagRequired("config"); err != nil {
		panic(err)
	}
}

func runS3Clear(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	verbose, _ := cmd.Flags().GetBool("verbose")

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	logger := logrus.New()
	if verbose {
		logger.SetLevel(logrus.DebugLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	if cfg.BlockDb.Engine != "s3" {
		return fmt.Errorf("blockDb.engine must be 's3' in config, got %q", cfg.BlockDb.Engine)
	}

	s3Cfg := cfg.BlockDb.S3
	if s3Cfg.Bucket == "" {
		return fmt.Errorf("blockDb.s3.bucket is required")
	}
	if s3Cfg.Path == "" {
		return fmt.Errorf("blockDb.s3.path is required (refusing to clear entire bucket)")
	}

	client, err := minio.New(s3Cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3Cfg.AccessKey, s3Cfg.SecretKey, ""),
		Secure: bool(s3Cfg.Secure),
		Region: s3Cfg.Region,
	})
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Verify bucket exists
	exists, err := client.BucketExists(ctx, s3Cfg.Bucket)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s3Cfg.Bucket)
	}

	prefix := s3Cfg.Path
	// Ensure prefix ends with / for proper listing
	if prefix != "" && prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	logger.WithFields(logrus.Fields{
		"bucket":   s3Cfg.Bucket,
		"prefix":   prefix,
		"endpoint": s3Cfg.Endpoint,
		"dryRun":   dryRun,
	}).Info("clearing S3 path")

	// List all objects under the prefix
	objectsCh := client.ListObjects(ctx, s3Cfg.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	if dryRun {
		return runS3ClearDryRun(logger, objectsCh)
	}

	return runS3ClearDelete(ctx, logger, client, s3Cfg.Bucket, objectsCh)
}

func runS3ClearDryRun(logger *logrus.Logger, objectsCh <-chan minio.ObjectInfo) error {
	var totalCount int64
	var totalSize int64

	for obj := range objectsCh {
		if obj.Err != nil {
			return fmt.Errorf("error listing objects: %w", obj.Err)
		}
		totalCount++
		totalSize += obj.Size
		logger.WithFields(logrus.Fields{
			"key":  obj.Key,
			"size": obj.Size,
		}).Debug("would delete")
	}

	logger.WithFields(logrus.Fields{
		"objects":   totalCount,
		"totalSize": formatBytes(totalSize),
	}).Info("dry run complete")

	return nil
}

func runS3ClearDelete(ctx context.Context, logger *logrus.Logger, client *minio.Client, bucket string, objectsCh <-chan minio.ObjectInfo) error {
	// Feed objects into a delete channel
	deleteCh := make(chan minio.ObjectInfo, 1000)
	var listErr error

	go func() {
		defer close(deleteCh)
		for obj := range objectsCh {
			if obj.Err != nil {
				listErr = fmt.Errorf("error listing objects: %w", obj.Err)
				return
			}
			deleteCh <- obj
		}
	}()

	var totalDeleted int64
	var totalSize int64

	for err := range client.RemoveObjects(ctx, bucket, deleteCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			logger.WithError(err.Err).WithField("key", err.ObjectName).Warn("failed to delete object")
			continue
		}
		totalDeleted++
	}

	if listErr != nil {
		return listErr
	}

	logger.WithFields(logrus.Fields{
		"deleted":   totalDeleted,
		"totalSize": formatBytes(totalSize),
	}).Info("S3 path cleared")

	return nil
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)

	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
