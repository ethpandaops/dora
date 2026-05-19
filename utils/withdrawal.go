package utils

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func ParseWithdrawalAddressOrCredentials(input string) (address []byte, credentials []byte, err error) {
	normalized := strings.TrimSpace(input)
	normalized = strings.TrimPrefix(normalized, "0x")
	normalized = strings.TrimPrefix(normalized, "0X")
	if normalized == "" {
		return nil, nil, fmt.Errorf("missing withdrawal address or credentials")
	}

	value, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid hex value")
	}

	switch len(value) {
	case 20:
		return value, nil, nil
	case 32:
		return nil, value, nil
	default:
		return nil, nil, fmt.Errorf("expected 20-byte withdrawal address or 32-byte withdrawal credentials")
	}
}

func WithdrawalCredentialsAddress(credentials []byte) []byte {
	if len(credentials) != 32 {
		return nil
	}
	if credentials[0] != 0x01 && credentials[0] != 0x02 {
		return nil
	}
	return credentials[12:]
}

func WithdrawalCredentialsGroup(credentials []byte) (string, string) {
	address := WithdrawalCredentialsAddress(credentials)
	if address == nil {
		return "no-address", ""
	}

	addressHex := hex.EncodeToString(address)
	return addressHex, "0x" + addressHex
}
