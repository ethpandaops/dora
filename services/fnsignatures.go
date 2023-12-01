package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	nethttp "net/http"

	"github.com/pk910/dora/db"
	"github.com/pk910/dora/dbtypes"
	"github.com/pk910/dora/types"
	"github.com/pk910/dora/utils"
	"github.com/sirupsen/logrus"
)

type TxSignaturesService struct {
}

var GlobalTxSignaturesService *TxSignaturesService
var logger_tss = logrus.StandardLogger().WithField("module", "txsig")

type TxSignaturesLookup struct {
	Bytes     types.TxSignatureBytes
	Signature string
	Name      string
	Status    types.TxSignatureLookupStatus
}

// StartTxSignaturesService is used to start the global transaction signatures service
func StartTxSignaturesService() error {
	if GlobalTxSignaturesService != nil {
		return nil
	}

	concurrencyLimit := utils.Config.TxSignature.ConcurrencyLimit
	if concurrencyLimit == 0 {
		concurrencyLimit = 10
	}

	GlobalTxSignaturesService = &TxSignaturesService{}

	if !utils.Config.TxSignature.DisableLookupLoop {
		go GlobalTxSignaturesService.runLookupLoop()
	}
	return nil
}

func (tss *TxSignaturesService) LookupSignatures(sigBytes []types.TxSignatureBytes) map[types.TxSignatureBytes]*TxSignaturesLookup {
	lookups := map[types.TxSignatureBytes]*TxSignaturesLookup{}
	unresolvedLookups := make([]*TxSignaturesLookup, 0)
	unresolvedLookupBytes := make([]types.TxSignatureBytes, 0)

	for i, bytes := range sigBytes {
		if lookups[bytes] != nil {
			continue
		}
		lookup := &TxSignaturesLookup{
			Bytes: bytes,
		}
		unresolvedLookups = append(unresolvedLookups, lookup)
		unresolvedLookupBytes = append(unresolvedLookupBytes, sigBytes[i])
		lookups[bytes] = lookup
	}

	// check known signatures in DB
	if len(unresolvedLookups) > 0 {
		for _, dbSigEntry := range db.GetTxFunctionSignaturesByBytes(unresolvedLookupBytes) {
			var lookup *TxSignaturesLookup
			for i, l := range unresolvedLookups {
				if l == nil {
					continue
				}
				if bytes.Equal(l.Bytes[:], dbSigEntry.Bytes) {
					lookup = l
					unresolvedLookups[i] = nil
					break
				}
			}
			if lookup == nil {
				break
			}
			lookup.Status = types.TxSigStatusFound
			lookup.Signature = dbSigEntry.Signature
			lookup.Name = dbSigEntry.Name
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		nonfoundLookupBytes := make([]types.TxSignatureBytes, 0)
		for i, l := range unresolvedLookups {
			if l != nil {
				nonfoundLookups = append(nonfoundLookups, unresolvedLookups[i])
				nonfoundLookupBytes = append(nonfoundLookupBytes, unresolvedLookups[i].Bytes)
			}
		}
		unresolvedLookups = nonfoundLookups
		unresolvedLookupBytes = nonfoundLookupBytes
	}

	// check unknown signatures in DB (previous failed sig lookups)
	if len(unresolvedLookups) > 0 {
		recheckTime := int64(utils.Config.TxSignature.RecheckTimeout.Seconds())
		if recheckTime == 0 {
			recheckTime = 86400
		}
		checkTimeout := time.Now().Unix() - recheckTime

		for _, unknownSigEntry := range db.GetUnknownFunctionSignatures(unresolvedLookupBytes) {
			if unknownSigEntry.LastCheck < uint64(checkTimeout) {
				break
			}

			var lookup *TxSignaturesLookup
			for i, l := range unresolvedLookups {
				if l == nil {
					continue
				}
				if bytes.Equal(l.Bytes[:], unknownSigEntry.Bytes) {
					lookup = l
					unresolvedLookups[i] = nil
					break
				}
			}
			if lookup == nil {
				break
			}
			lookup.Status = types.TxSigStatusUnknown
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		for _, l := range unresolvedLookups {
			if l == nil {
				break
			}
			nonfoundLookups = append(nonfoundLookups, l)
		}
		unresolvedLookups = nonfoundLookups
	}

	// add pending signature lookups
	if len(unresolvedLookups) > 0 && !utils.Config.TxSignature.DisableLookupLoop {
		pendingLookups := make([]*dbtypes.TxPendingFunctionSignature, 0)

		for _, l := range unresolvedLookups {
			pendingLookups = append(pendingLookups, &dbtypes.TxPendingFunctionSignature{
				Bytes:     l.Bytes[:],
				QueueTime: uint64(time.Now().Unix()),
			})
		}

		//logger_tss.Infof("starting db transaction (pending sig)")
		tx, err := db.WriterDb.Beginx()
		if err != nil {
			logger_tss.Warnf("error starting db transaction: %v", err)
		} else {
			defer tx.Rollback()

			err := db.InsertPendingFunctionSignatures(pendingLookups, tx)
			if err != nil {
				logger_tss.Warnf("error saving pending signature: %v", err)
			}

			//logger_tss.Infof("stopping db transaction (pending sig)")
			if err := tx.Commit(); err != nil {
				logger_tss.Warnf("error committing db transaction: %v", err)
			}
		}

	}

	return lookups
}

func (tss *TxSignaturesService) runLookupLoop() {
	defer utils.HandleSubroutinePanic("txsig.loop")

	loopInterval := utils.Config.TxSignature.LookupInterval
	if loopInterval == 0 {
		loopInterval = 10 * time.Second
	}

	for {
		//logger_tss.Infof("tx signatures processing loop")
		startTime := time.Now()
		tss.processPendingSignatures()

		loopDelay := time.Since(startTime)
		if loopDelay < loopInterval {
			time.Sleep(loopInterval - loopDelay)
		}
	}
}

func (tss *TxSignaturesService) processPendingSignatures() {
	batchLimit := utils.Config.TxSignature.LookupBatchSize
	if batchLimit == 0 {
		batchLimit = 10
	}
	pendingSigs := db.GetPendingFunctionSignatures(batchLimit)

	wg := sync.WaitGroup{}
	lookups := make([]*TxSignaturesLookup, 0)
	for _, pendingSig := range pendingSigs {
		lookup := &TxSignaturesLookup{
			Bytes: types.TxSignatureBytes(pendingSig.Bytes),
		}
		lookups = append(lookups, lookup)

		wg.Add(1)
		go func(lookup *TxSignaturesLookup) {
			defer utils.HandleSubroutinePanic("txsig.lookup")
			err := tss.lookupSignature(lookup)
			if err != nil {
				logger_tss.Warnf("tx signatures lookup failed: %v", err)
			}

			wg.Done()
		}(lookup)
	}
	wg.Wait()

	// store new lookup results to db
	pendingSigBytes := []types.TxSignatureBytes{}
	unknownSigs := []*dbtypes.TxUnknownFunctionSignature{}
	resolvedSigs := []*dbtypes.TxFunctionSignature{}
	for _, lookup := range lookups {
		if lookup.Status == types.TxSigStatusPending {
			continue
		}
		pendingSigBytes = append(pendingSigBytes, lookup.Bytes)

		if lookup.Status == types.TxSigStatusUnknown {
			unknownSigs = append(unknownSigs, &dbtypes.TxUnknownFunctionSignature{
				Bytes:     lookup.Bytes[:],
				LastCheck: uint64(time.Now().Unix()),
			})
		} else if lookup.Status == types.TxSigStatusFound {
			resolvedSigs = append(resolvedSigs, &dbtypes.TxFunctionSignature{
				Bytes:     lookup.Bytes[:],
				Signature: lookup.Signature,
				Name:      lookup.Name,
			})
		}
	}

	if len(pendingSigBytes) > 0 {
		tx, err := db.WriterDb.Beginx()
		if err != nil {
			logger_tss.Warnf("error starting db transaction: %v", err)
		} else {
			defer tx.Rollback()

			err := db.DeletePendingFunctionSignatures(pendingSigBytes, tx)
			if err != nil {
				logger_tss.Warnf("error deleting pending signature: %v", err)
			}

			if len(unknownSigs) > 0 {
				err := db.InsertUnknownFunctionSignatures(unknownSigs, tx)
				if err != nil {
					logger_tss.Warnf("error saving unknown signature: %v", err)
				}
			}
			for _, fnsig := range resolvedSigs {
				err := db.InsertTxFunctionSignature(fnsig, tx)
				if err != nil {
					logger_tss.Warnf("error saving resolved signature: %v", err)
				}
			}

			if err := tx.Commit(); err != nil {
				logger_tss.Warnf("error committing db transaction: %v", err)
			}
		}
	}
}

func (tss *TxSignaturesService) lookupSignature(lookup *TxSignaturesLookup) error {
	var resErr error

	if !utils.Config.TxSignature.Disable4Bytes {
		err := tss.lookup4Bytes(lookup)
		if err != nil {
			resErr = fmt.Errorf("4bytes lookup failed: %w", err)
		} else if lookup.Status == types.TxSigStatusFound {
			return nil
		}
	}

	logger_tss.Debugf("lookup fn signature 0x%x (%v): %v", lookup.Bytes[:], lookup.Status, lookup.Signature)

	return resErr
}

type txSigLookup_4bytesResponse struct {
	Count   int `json:"count"`
	Results []struct {
		Id        int    `json:"id"`
		Signature string `json:"text_signature"`
	} `json:"results"`
}

func (tss *TxSignaturesService) lookup4Bytes(lookup *TxSignaturesLookup) error {
	// lookup signature via https://www.4byte.directory/
	url := fmt.Sprintf("https://www.4byte.directory/api/v1/signatures/?format=json&hex_signature=0x%x", lookup.Bytes)

	req, err := nethttp.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	client := &nethttp.Client{Timeout: time.Second * 10}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != nethttp.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, code: %v, error-response: %s", url, resp.StatusCode, data)
	}

	returnValue := txSigLookup_4bytesResponse{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&returnValue)
	if err != nil {
		return fmt.Errorf("error parsing 4bytes json response: %v", err)
	}

	if returnValue.Count == 0 {
		lookup.Status = types.TxSigStatusUnknown
	} else {
		lookup.Status = types.TxSigStatusFound
		lookup.Signature = returnValue.Results[0].Signature
		sigparts := strings.Split(lookup.Signature, "(")
		lookup.Name = sigparts[0]
	}
	return nil
}
