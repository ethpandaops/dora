package indexer

import (
	"fmt"
	"os"
	"path"

	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/aws"
	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var logger_blobs = logrus.StandardLogger().WithField("module", "blobstore")

const (
	blobEngineNone uint64 = 0
	blobEngineDb   uint64 = 1
	blobEngineFs   uint64 = 2
	blobEngineAws  uint64 = 3
)

type BlobStore struct {
	engine  uint64
	s3Store *aws.S3Store
}

func newBlobStore() *BlobStore {
	store := &BlobStore{}

	switch utils.Config.BlobStore.Engine {
	case "none":
		store.engine = blobEngineNone
	case "db":
		store.engine = blobEngineDb
	case "fs":
		if utils.Config.BlobStore.Fs.Path == "" {
			logger_blobs.Errorf("cannot init blobstore with 'fs' engine: missing path")
			break
		}
		store.engine = blobEngineFs
	case "aws":
		s3store, err := aws.NewS3Store(utils.Config.BlobStore.Aws.AccessKey, utils.Config.BlobStore.Aws.SecretKey, utils.Config.BlobStore.Aws.S3Region, utils.Config.BlobStore.Aws.S3Bucket)
		if err != nil {
			logger_blobs.Errorf("cannot init blobstore with 'aws' engine: %v", err)
			break
		}
		store.engine = blobEngineAws
		store.s3Store = s3store
	}
	return store
}

func (store *BlobStore) saveBlob(blob *rpctypes.BlobSidecar, tx *sqlx.Tx) error {
	dbBlob := &dbtypes.Blob{
		Commitment: blob.KzgCommitment,
		Slot:       uint64(blob.Slot),
		Root:       blob.BlockRoot,
		Proof:      blob.KzgProof,
		Size:       uint32(len(blob.Blob)),
	}
	switch store.engine {
	case blobEngineNone:
		return nil
	case blobEngineDb:
		dbBlob.Blob = (*[]byte)(&blob.Blob)
	case blobEngineFs:
		fsName := fmt.Sprintf("%v%x.blob", utils.Config.BlobStore.Fs.Prefix, dbBlob.Commitment)
		storage := fmt.Sprintf("fs:%v", fsName)
		dbBlob.Storage = &storage
		blobFile := path.Join(utils.Config.BlobStore.Fs.Path, fsName)
		err := os.WriteFile(blobFile, blob.Blob, 0644)
		if err != nil {
			return fmt.Errorf("could not open blob file '%v': %w", blobFile, err)
		}
	case blobEngineAws:
		s3Key := fmt.Sprintf("%v-%x", dbBlob.Slot, dbBlob.Commitment)
		storage := fmt.Sprintf("aws:%v", s3Key)
		dbBlob.Storage = &storage
		err := store.s3Store.Upload(s3Key, blob.Blob)
		if err != nil {
			return fmt.Errorf("could not upload blob to s3 '%v': %w", s3Key, err)
		}
	}

	err := db.InsertBlob(dbBlob, tx)
	if err != nil {
		return fmt.Errorf("could not add blob to db: %w", err)
	}

	return nil
}
