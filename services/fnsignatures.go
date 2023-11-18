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
	"github.com/pk910/dora/utils"
	"github.com/sirupsen/logrus"
)

type TxSignatureBytes [4]byte
type TxSignatureLookupStatus uint8

var (
	TxSigStatusPending TxSignatureLookupStatus = 0
	TxSigStatusFound   TxSignatureLookupStatus = 1
	TxSigStatusUnknown TxSignatureLookupStatus = 2
)

type TxSignaturesService struct {
	mutex           sync.Mutex
	lookupMap       map[TxSignatureBytes]*TxSignaturesLookup
	concurrencyChan chan bool
}

var GlobalTxSignaturesService *TxSignaturesService
var logger_tss = logrus.StandardLogger().WithField("module", "txsig")

type TxSignaturesLookup struct {
	Bytes     []byte
	Signature string
	Name      string
	Status    TxSignatureLookupStatus
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
		lookupMap:       map[TxSignatureBytes]*TxSignaturesLookup{},
		concurrencyChan: make(chan bool, concurrencyLimit),
	}
	return nil
}

func (tss *TxSignaturesService) LookupSignatures(sigBytes []TxSignatureBytes) map[TxSignatureBytes]*TxSignaturesLookup {
	lookups := map[TxSignatureBytes]*TxSignaturesLookup{}
	unresolvedLookups := make([]*TxSignaturesLookup, 0)
	unresolvedLookupBytes := make([][]byte, 0)

	tss.mutex.Lock()
	for _, bytes := range sigBytes {
		lookup := tss.lookupMap[bytes]
		if lookup == nil {
			lookup = &TxSignaturesLookup{
				Bytes: bytes[:],
			}
			unresolvedLookups = append(unresolvedLookups, lookup)
			unresolvedLookupBytes = append(unresolvedLookupBytes, bytes[:])
		}
		lookups[bytes] = lookup
	}
	tss.mutex.Unlock()

	// check known signatures in DB
	if len(unresolvedLookups) > 0 {
		for _, dbSigEntry := range db.GetTxFunctionSignaturesByBytes(unresolvedLookupBytes) {
			var lookup *TxSignaturesLookup
			for i, l := range unresolvedLookups {
				if bytes.Equal(l.Bytes, dbSigEntry.Bytes) {
					lookup = l
					unresolvedLookups[i] = nil
					break
				}
			}
			if lookup == nil {
				break
			}
			lookup.Status = TxSigStatusFound
			lookup.Signature = dbSigEntry.Signature
			lookup.Name = dbSigEntry.Name
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		nonfoundLookupBytes := make([][]byte, 0)
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
				if bytes.Equal(l.Bytes, unknownSigEntry.Bytes) {
					lookup = l
					unresolvedLookups[i] = nil
					break
				}
			}
			if lookup == nil {
				break
			}
			lookup.Status = TxSigStatusUnknown
		}

		nonfoundLookups := make([]*TxSignaturesLookup, 0)
		nonfoundLookupBytes := make([][]byte, 0)
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

				tss.lookupSignature(lookup)
			}(lookup)
		}
		wg.Wait()

		// store new lookup results to db
		unknownSigs := []*dbtypes.TxUnknownFunctionSignature{}
		resolvedSigs := []*dbtypes.TxFunctionSignature{}

		for _, lookup := range unresolvedLookups {
			if lookup.Status == TxSigStatusUnknown {
				unknownSigs = append(unknownSigs, &dbtypes.TxUnknownFunctionSignature{
					Bytes:     lookup.Bytes,
					LastCheck: uint64(time.Now().Unix()),
				})
			} else if lookup.Status == TxSigStatusFound {
				resolvedSigs = append(resolvedSigs, &dbtypes.TxFunctionSignature{
					Bytes:     lookup.Bytes,
					Signature: lookup.Signature,
					Name:      lookup.Name,
				})
			}
		}
	}

	return lookups
}

func (tss *TxSignaturesService) lookupSignature(lookup *TxSignaturesLookup) error {
	if utils.Config.TxSignature.Enable4Bytes {
		err := tss.lookup4Bytes(lookup)
		if err != nil {
			logger_tss.Warnf("tx signatures lookup from 4bytes failed: %v", err)
		} else if lookup.Status == TxSigStatusFound {
			return nil
		}
	}

	return nil
}

type txSigLookup_4bytesResponse struct {
	Count   int `json:"count"`
	Results []struct {
		Id        int    `json:"id"`
		Signature string `json:"textsignature"`
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
		lookup.Status = TxSigStatusUnknown
	} else {
		lookup.Status = TxSigStatusFound
		lookup.Signature = returnValue.Results[0].Signature
		sigparts := strings.Split(lookup.Signature, "(")
		lookup.Name = sigparts[0]
	}
	return nil
}
