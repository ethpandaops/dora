package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseWithdrawalAddressOrCredentials(t *testing.T) {
	addressInput := "0x" + strings.Repeat("11", 20)
	address, creds, err := ParseWithdrawalAddressOrCredentials(addressInput)
	if err != nil {
		t.Fatalf("expected address parse to succeed: %v", err)
	}
	if len(address) != 20 || len(creds) != 0 {
		t.Fatalf("expected 20-byte address and empty credentials, got %d/%d", len(address), len(creds))
	}

	credentialsInput := "0X01" + strings.Repeat("00", 11) + strings.Repeat("22", 20)
	address, creds, err = ParseWithdrawalAddressOrCredentials(credentialsInput)
	if err != nil {
		t.Fatalf("expected credentials parse to succeed: %v", err)
	}
	if len(address) != 0 || len(creds) != 32 {
		t.Fatalf("expected empty address and 32-byte credentials, got %d/%d", len(address), len(creds))
	}

	_, _, err = ParseWithdrawalAddressOrCredentials("0x1234")
	if err == nil {
		t.Fatal("expected short input to fail")
	}
}

func TestWithdrawalCredentialsHelpers(t *testing.T) {
	address := bytes.Repeat([]byte{0x22}, 20)
	credentials := append([]byte{0x01}, bytes.Repeat([]byte{0x00}, 11)...)
	credentials = append(credentials, address...)

	extracted := WithdrawalCredentialsAddress(credentials)
	if !bytes.Equal(extracted, address) {
		t.Fatalf("expected extracted address %x, got %x", address, extracted)
	}

	groupKey, groupName := WithdrawalCredentialsGroup(credentials)
	if groupKey != strings.Repeat("22", 20) || groupName != "0x"+strings.Repeat("22", 20) {
		t.Fatalf("unexpected group %q/%q", groupKey, groupName)
	}

	blsCredentials := append([]byte{0x00}, bytes.Repeat([]byte{0x33}, 31)...)
	groupKey, groupName = WithdrawalCredentialsGroup(blsCredentials)
	if groupKey != "no-address" || groupName != "" {
		t.Fatalf("expected no-address group, got %q/%q", groupKey, groupName)
	}

	if !bytes.Equal(WithdrawalCredentialsAddress(credentials), address) {
		t.Fatal("expected address match")
	}
}
