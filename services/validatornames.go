package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type ValidatorNames struct {
	namesMutex sync.RWMutex
	names      map[uint64]string
}

func (vn *ValidatorNames) GetValidatorName(index uint64) string {
	vn.namesMutex.RLock()
	defer vn.namesMutex.RUnlock()
	if vn.names == nil {
		return ""
	}
	return vn.names[index]
}

func (vn *ValidatorNames) LoadFromYaml(fileName string) error {
	vn.namesMutex.Lock()
	defer vn.namesMutex.Unlock()

	f, err := os.Open(fileName)
	if err != nil {
		logrus.Errorf("error opening validator names file %v: %v", fileName, err)
		return fmt.Errorf("error opening validator names file %v: %v", fileName, err)
	}

	namesYaml := map[string]string{}
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&namesYaml)
	if err != nil {
		logrus.Errorf("error decoding validator names file %v: %v", fileName, err)
		return fmt.Errorf("error decoding validator names file %v: %v", fileName, err)
	}

	nameCount := 0
	if vn.names == nil {
		vn.names = make(map[uint64]string)
	}
	for idxStr, name := range namesYaml {
		rangeParts := strings.Split(idxStr, "-")
		minIdx, err := strconv.ParseUint(rangeParts[0], 10, 64)
		if err != nil {
			continue
		}
		maxIdx := minIdx + 1
		if len(rangeParts) > 1 {
			maxIdx, err = strconv.ParseUint(rangeParts[1], 10, 64)
			if err != nil {
				continue
			}
		}
		for idx := minIdx; idx <= maxIdx; idx++ {
			vn.names[idx] = name
			nameCount++
		}
	}
	logrus.Infof("Loaded %v validator names from yaml (%v)", nameCount, fileName)

	return nil
}

type validatorNamesRangesResponse struct {
	Ranges map[string]string `json:"ranges"`
}

func (vn *ValidatorNames) LoadFromRangesApi(apiUrl string) error {
	vn.namesMutex.Lock()
	defer vn.namesMutex.Unlock()
	logrus.Debugf("Loading validator names from inventory: %v", apiUrl)

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Get(apiUrl)
	if err != nil {
		logrus.Errorf("Could not fetch validator names from inventory (%v): %v", apiUrl, err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			logrus.Errorf("Could not fetch validator names from inventory (%v): not found", apiUrl)
			return nil
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", apiUrl, data)
	}
	rangesResponse := &validatorNamesRangesResponse{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&rangesResponse)
	if err != nil {
		return fmt.Errorf("error parsing validator ranges response: %v", err)
	}

	if vn.names == nil {
		vn.names = make(map[uint64]string)
	}
	nameCount := 0
	for rangeStr, name := range rangesResponse.Ranges {
		rangeParts := strings.Split(rangeStr, "-")
		minIdx, err := strconv.ParseUint(rangeParts[0], 10, 64)
		if err != nil {
			continue
		}
		maxIdx := minIdx + 1
		if len(rangeParts) > 1 {
			maxIdx, err = strconv.ParseUint(rangeParts[1], 10, 64)
			if err != nil {
				continue
			}
		}
		for idx := minIdx; idx <= maxIdx; idx++ {
			vn.names[idx] = name
			nameCount++
		}
	}
	logrus.Infof("Loaded %v validator names from inventory api (%v)", nameCount, apiUrl)
	return nil
}
