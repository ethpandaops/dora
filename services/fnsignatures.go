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
	mutex           sync.Mutex
	lookupMap       map[types.TxSignatureBytes]*TxSignaturesLookup
	concurrencyChan chan bool
}

var GlobalTxSignaturesService *TxSignaturesService
var logger_tss = logrus.StandardLogger().WithField("module", "txsig")

type TxSignaturesLookup struct {
	mutex     sync.RWMutex
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

	GlobalTxSignaturesService = &TxSignaturesService{
		lookupMap:       map[types.TxSignatureBytes]*TxSignaturesLookup{},
		concurrencyChan: make(chan bool, concurrencyLimit),
	}
	return nil
}

func (tss *TxSignaturesService) LookupSignatures(sigBytes []types.TxSignatureBytes) map[types.TxSignatureBytes]*TxSignaturesLookup {
	lookups := map[types.TxSignatureBytes]*TxSignaturesLookup{}
	var newLookups []*TxSignaturesLookup
	unresolvedLookups := make([]*TxSignaturesLookup, 0)
	unresolvedLookupBytes := make([]types.TxSignatureBytes, 0)

	tss.mutex.Lock()
	for _, bytes := range sigBytes {
		lookup := tss.lookupMap[bytes]
		if lookup == nil {
			lookup = &TxSignaturesLookup{
				Bytes: bytes,
			}
			lookup.mutex.Lock()
			unresolvedLookups = append(unresolvedLookups, lookup)
			unresolvedLookupBytes = append(unresolvedLookupBytes, bytes)
			tss.lookupMap[bytes] = lookup
		}
		lookups[bytes] = lookup
	}
	tss.mutex.Unlock()
	newLookups = unresolvedLookups

	// check known signatures in DB
	if len(unresolvedLookups) > 0 {
		for _, dbSigEntry := range db.GetTxFunctionSignaturesByBytes(unresolvedLookupBytes) {
			var lookup *TxSignaturesLookup
			for i, l := range unresolvedLookups {
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
			lookup.mutex.Unlock()
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		nonfoundLookupBytes := make([]types.TxSignatureBytes, 0)
		for _, l := range unresolvedLookups {
			if l == nil {
				break
			}
			nonfoundLookups = append(nonfoundLookups, l)
			nonfoundLookupBytes = append(nonfoundLookupBytes, l.Bytes)
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
			lookup.mutex.Unlock()
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		nonfoundLookupBytes := make([]types.TxSignatureBytes, 0)
		for _, l := range unresolvedLookups {
			if l == nil {
				break
			}
			nonfoundLookups = append(nonfoundLookups, l)
			nonfoundLookupBytes = append(nonfoundLookupBytes, l.Bytes)
		}
		unresolvedLookups = nonfoundLookups
		unresolvedLookupBytes = nonfoundLookupBytes
	}

	// do signature lookups
	if len(unresolvedLookups) > 0 {
		wg := sync.WaitGroup{}
		for _, lookup := range unresolvedLookups {
			wg.Add(1)
			go func(lookup *TxSignaturesLookup) {
				tss.concurrencyChan <- true
				defer func() {
					wg.Done()
					<-tss.concurrencyChan
				}()

				err := tss.lookupSignature(lookup)
				if err != nil {
					logger_tss.Warnf("tx signatures lookup failed: %v", err)
				}
			}(lookup)
		}
		wg.Wait()

		// store new lookup results to db
		unknownSigs := []*dbtypes.TxUnknownFunctionSignature{}
		resolvedSigs := []*dbtypes.TxFunctionSignature{}
		for _, lookup := range unresolvedLookups {
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
			lookup.mutex.Unlock()
		}

		if len(unknownSigs) > 0 || len(resolvedSigs) > 0 {
			tx, err := db.WriterDb.Beginx()
			if err != nil {
				logger_tss.Warnf("error starting db transaction: %v", err)
			} else {
				defer tx.Rollback()

				if len(unknownSigs) > 0 {
					err := db.InsertUnknownFunctionSignatures(unknownSigs, tx)
					if err != nil {
						logger_tss.Warnf("error saving resolved signature: %v", err)
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

	// remove from pending lookup map
	tss.mutex.Lock()
	for _, lookup := range newLookups {
		delete(tss.lookupMap, lookup.Bytes)
	}
	tss.mutex.Unlock()

	return lookups
}

func (tss *TxSignaturesService) lookupSignature(lookup *TxSignaturesLookup) error {
	var resErr error

	if utils.Config.TxSignature.Enable4Bytes {
		err := tss.lookup4Bytes(lookup)
		if err != nil {
			resErr = fmt.Errorf("4bytes lookup failed: %w", err)
		} else if lookup.Status == types.TxSigStatusFound {
			return nil
		}
	}

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
