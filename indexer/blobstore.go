package indexer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/pk910/dora/aws"
	"github.com/pk910/dora/db"
	"github.com/pk910/dora/dbtypes"
	"github.com/pk910/dora/utils"
)

var logger_blobs = logrus.StandardLogger().WithField("module", "blobstore")

const (
	blobPersistenceModeNone uint64 = 0
	blobPersistenceModeDb   uint64 = 1
	blobPersistenceModeFs   uint64 = 2
	blobPersistenceModeAws  uint64 = 3
)

type BlobStore struct {
	mode    uint64
	s3Store *aws.S3Store
}

func newBlobStore() *BlobStore {
	store := &BlobStore{}

	switch utils.Config.BlobStore.PersistenceMode {
	case "none":
		store.mode = blobPersistenceModeNone
	case "db":
		store.mode = blobPersistenceModeDb
	case "fs":
		if utils.Config.BlobStore.Fs.Path == "" {
			logger_blobs.Errorf("cannot init blobstore with 'fs' engine: missing path")
			break
		}
		os.Mkdir(utils.Config.BlobStore.Fs.Path, 0755)
		store.mode = blobPersistenceModeFs
	case "aws":
		s3store, err := aws.NewS3Store(utils.Config.BlobStore.Aws.AccessKey, utils.Config.BlobStore.Aws.SecretKey, utils.Config.BlobStore.Aws.S3Region, utils.Config.BlobStore.Aws.S3Bucket)
		if err != nil {
			logger_blobs.Errorf("cannot init blobstore with 'aws' engine: %v", err)
			break
		}
		store.mode = blobPersistenceModeAws
		store.s3Store = s3store
	}
	return store
}

func (store *BlobStore) getBlobName(blob *dbtypes.Blob) string {
	blobName := utils.Config.BlobStore.NameTemplate
	blobName = strings.ReplaceAll(blobName, "{commitment}", fmt.Sprintf("%x", blob.Commitment))

	versionedHash := sha256.Sum256(blob.Commitment)
	versionedHash[0] = 0x01
	blobName = strings.ReplaceAll(blobName, "{hash}", fmt.Sprintf("%x", versionedHash))
	return blobName
}

func (store *BlobStore) saveBlob(blob *deneb.BlobSidecar, tx *sqlx.Tx) error {
	dbBlob := &dbtypes.Blob{
		Commitment: blob.KzgCommitment[:],
		Proof:      blob.KzgProof[:],
		Size:       uint32(len(blob.Blob)),
	}
	dbBlobAssignment := &dbtypes.BlobAssignment{
		Root:       blob.BlockRoot[:],
		Commitment: blob.KzgCommitment[:],
		Slot:       uint64(blob.Slot),
	}
	blobName := store.getBlobName(dbBlob)

	switch store.mode {
	case blobPersistenceModeDb:
		blobData := blob.Blob[:]
		dbBlob.Blob = &blobData
	case blobPersistenceModeFs:
		blobFile := path.Join(utils.Config.BlobStore.Fs.Path, blobName)
		err := os.WriteFile(blobFile, blob.Blob[:], 0644)
		if err != nil {
			return fmt.Errorf("could not save blob to file '%v': %w", blobFile, err)
		}
	case blobPersistenceModeAws:
		err := store.s3Store.Upload(blobName, blob.Blob[:])
		if err != nil {
			return fmt.Errorf("could not upload blob to s3 '%v': %w", blobName, err)
		}
	}

	err := db.InsertBlob(dbBlob, tx)
	if err != nil {
		return fmt.Errorf("could not add blob to db: %w", err)
	}
	err = db.InsertBlobAssignment(dbBlobAssignment, tx)
	if err != nil {
		return fmt.Errorf("could not add blob assignment to db: %w", err)
	}

	return nil
}

func (store *BlobStore) LoadBlob(commitment []byte, blockroot []byte, client *IndexerClient) (*dbtypes.Blob, error) {
	dbBlob := db.GetBlob(commitment, true)
	if dbBlob != nil {
		blobName := store.getBlobName(dbBlob)

		if dbBlob.Blob == nil {
			switch store.mode {
			case blobPersistenceModeFs:
				blobFile := path.Join(utils.Config.BlobStore.Fs.Path, blobName)
				data, err := os.ReadFile(blobFile)
				if err != nil {
					logger_blobs.Warnf("cannot load blob from fs (%v): %v", blobFile, err)
				} else {
					dbBlob.Blob = &data
				}
			case blobPersistenceModeAws:
				data, err := store.s3Store.Download(blobName)
				if err != nil {
					logger_blobs.Warnf("cannot load blob from aws (%v): %v", blobName, err)
				} else {
					dbBlob.Blob = &data
				}
			}
		}
	}

	if (dbBlob == nil || dbBlob.Blob == nil) && client != nil {
		if blockroot == nil {
			latestAssignment := db.GetLatestBlobAssignment(dbBlob.Commitment)
			if latestAssignment != nil {
				blockroot = latestAssignment.Root
			}
		}

		if blockroot != nil {
			// load from rpc
			var blob *deneb.BlobSidecar
			blobRsp, err := client.rpcClient.GetBlobSidecarsByBlockroot(blockroot)
			if err != nil {
				logger_blobs.Warnf("cannot load blobs from rpc (0x%x): %v", blockroot, err)
			} else {
				for _, cblob := range blobRsp {
					if bytes.Equal(cblob.KzgCommitment[:], commitment) {
						blob = cblob
						break
					}
				}
			}

			if blob != nil {
				blobData := blob.Blob[:]
				if dbBlob == nil {
					dbBlob = &dbtypes.Blob{
						Commitment: commitment,
						Proof:      blob.KzgProof[:],
						Size:       uint32(len(blobData)),
					}
				}
				dbBlob.Blob = &blobData
			}
		}
	}

	return dbBlob, nil
}
