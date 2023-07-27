package services

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type ValidatorNames struct {
	namesMutex sync.RWMutex
	names      map[uint64]string
}

type validatorNamesYaml struct {
	ValidatorNames map[string]string `yaml:"validatorNames"`
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
		return fmt.Errorf("error opening validator names file %v: %v", fileName, err)
	}
	namesYaml := &validatorNamesYaml{}
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&namesYaml)
	if err != nil {
		return fmt.Errorf("error decoding validator names file %v: %v", fileName, err)
	}
	logrus.Infof("Loaded validator names (%v entries)", len(namesYaml.ValidatorNames))

	vn.names = make(map[uint64]string)
	for idxStr, name := range namesYaml.ValidatorNames {
		idx, err := strconv.ParseUint(idxStr, 10, 64)
		if err != nil {
			continue
		}
		vn.names[idx] = name
	}
	return nil
}
