package services

import (
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestGetValidatorName(t *testing.T) {
	// Create a test validator name entry
	testEntry := &validatorNameEntry{name: "test-validator"}

	tests := []struct {
		name                string
		index               uint64
		setupValidatorNames func() *ValidatorNames
		expectedResult      string
	}{
		{
			name:  "returns empty when namesByIndex is nil",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex: sync.RWMutex{},
				}
			},
			expectedResult: "",
		},
		{
			name:  "returns name from namesByIndex when found",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:   sync.RWMutex{},
					namesByIndex: map[uint64]*validatorNameEntry{123: testEntry},
				}
			},
			expectedResult: "test-validator",
		},
		{
			name:  "returns empty when resolvedNamesByIndex is nil",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:   sync.RWMutex{},
					namesByIndex: map[uint64]*validatorNameEntry{},
				}
			},
			expectedResult: "",
		},
		{
			name:  "returns name from resolvedNamesByIndex when found",
			index: 123,
			setupValidatorNames: func() *ValidatorNames {
				return &ValidatorNames{
					namesMutex:           sync.RWMutex{},
					namesByIndex:         map[uint64]*validatorNameEntry{},
					resolvedNamesByIndex: map[uint64]*validatorNameEntry{123: testEntry},
				}
			},
			expectedResult: "test-validator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vn := tt.setupValidatorNames()

			result := vn.GetValidatorName(tt.index)

			if result != tt.expectedResult {
				t.Errorf("GetValidatorName() = %q, want %q", result, tt.expectedResult)
			}
		})
	}
}

func TestGetValidatorName_Concurrency(t *testing.T) {
	vn := &ValidatorNames{
		namesMutex:           sync.RWMutex{},
		namesByIndex:         make(map[uint64]*validatorNameEntry),
		namesByWithdrawal:    make(map[common.Address]*validatorNameEntry),
		namesByDepositOrigin: make(map[common.Address]*validatorNameEntry),
		namesByDepositTarget: make(map[common.Address]*validatorNameEntry),
		resolvedNamesByIndex: make(map[uint64]*validatorNameEntry),
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Goroutine 1: Continuous Reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = vn.GetValidatorName(123)
			}
		}
	}()

	// Goroutine 2: Continuous Loader-like Map Reset/Updates
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				vn.namesMutex.Lock()
				vn.namesByIndex = make(map[uint64]*validatorNameEntry)
				vn.namesByIndex[123] = &validatorNameEntry{name: "test"}
				vn.namesMutex.Unlock()
			}
		}
	}()

	// Goroutine 3: Continuous Resolver-like Map Writes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				newResolved := make(map[uint64]*validatorNameEntry)
				newResolved[123] = &validatorNameEntry{name: "resolved"}
				vn.namesMutex.Lock()
				vn.resolvedNamesByIndex = newResolved
				vn.namesMutex.Unlock()
			}
		}
	}()

	// Run for a short duration
	closeChan := make(chan struct{})
	go func() {
		select {
		case <-time.After(100 * time.Millisecond):
			close(stop)
			close(closeChan)
		}
	}()

	<-closeChan
	wg.Wait()
}
