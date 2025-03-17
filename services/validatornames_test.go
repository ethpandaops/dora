package services

import (
	"sync"
	"testing"
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
