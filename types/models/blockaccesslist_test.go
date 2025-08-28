package models

import (
	"testing"
)

func TestGetDisplayIndex(t *testing.T) {
	tests := []struct {
		name     string
		index    uint16
		txCount  int
		expected string
	}{
		{
			name:     "pre-execution block access index",
			index:    0,
			txCount:  10,
			expected: "pre-execution",
		},
		{
			name:     "first transaction",
			index:    1,
			txCount:  10,
			expected: "tx-1",
		},
		{
			name:     "middle transaction",
			index:    5,
			txCount:  10,
			expected: "tx-5",
		},
		{
			name:     "last transaction",
			index:    10,
			txCount:  10,
			expected: "tx-10",
		},
		{
			name:     "post-execution block access index",
			index:    11,
			txCount:  10,
			expected: "post-execution",
		},
		{
			name:     "post-execution with zero transactions",
			index:    1,
			txCount:  0,
			expected: "post-execution",
		},
		{
			name:     "single transaction block",
			index:    1,
			txCount:  1,
			expected: "tx-1",
		},
		{
			name:     "post-execution single transaction block",
			index:    2,
			txCount:  1,
			expected: "post-execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetDisplayIndex(tt.index, tt.txCount)
			if result != tt.expected {
				t.Errorf("GetDisplayIndex(%d, %d) = %s, want %s", tt.index, tt.txCount, result, tt.expected)
			}
		})
	}
}

func TestBlockAccessListStructures(t *testing.T) {
	// Test creating a simple BAL structure
	bal := &BlockAccessList{
		Hash:            []byte{0x12, 0x34},
		TotalAccounts:   2,
		TotalSlots:      3,
		TotalStorageOps: 5,
		AccountChanges: []*AccountChanges{
			{
				Address: []byte{0xaa, 0xbb, 0xcc},
				StorageChanges: []*SlotChanges{
					{
						Slot: []byte{0x00, 0x01},
						Changes: []*StorageChange{
							{
								BlockAccessIndex: 1,
								NewValue:         []byte{0xff},
								DisplayIndex:     "tx-1",
							},
						},
					},
				},
				StorageReads: [][]byte{{0x00, 0x02}},
				BalanceChanges: []*BalanceChange{
					{
						BlockAccessIndex: 1,
						PostBalance:      1000000000000000000,
						DisplayIndex:     "tx-1",
					},
				},
			},
		},
	}

	// Validate structure
	if len(bal.AccountChanges) != 1 {
		t.Errorf("Expected 1 account change, got %d", len(bal.AccountChanges))
	}

	account := bal.AccountChanges[0]
	if len(account.StorageChanges) != 1 {
		t.Errorf("Expected 1 storage change, got %d", len(account.StorageChanges))
	}

	if len(account.StorageReads) != 1 {
		t.Errorf("Expected 1 storage read, got %d", len(account.StorageReads))
	}

	if len(account.BalanceChanges) != 1 {
		t.Errorf("Expected 1 balance change, got %d", len(account.BalanceChanges))
	}

	// Verify display indices
	if account.StorageChanges[0].Changes[0].DisplayIndex != "tx-1" {
		t.Errorf("Expected display index 'tx-1', got %s", account.StorageChanges[0].Changes[0].DisplayIndex)
	}

	if account.BalanceChanges[0].DisplayIndex != "tx-1" {
		t.Errorf("Expected balance change display index 'tx-1', got %s", account.BalanceChanges[0].DisplayIndex)
	}
}
