package rpc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LenientUint64 accepts either a JSON number (e.g. 1) or a JSON string
// (e.g. "0x1" or "1") and decodes it into a uint64.
type LenientUint64 uint64

func (u *LenientUint64) UnmarshalJSON(input []byte) error {
	// null
	if len(input) == 4 && string(input) == "null" {
		*u = 0
		return nil
	}

	// Try string first
	var s string
	if err := json.Unmarshal(input, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			*u = 0
			return nil
		}

		base := 10
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			base = 16
			s = s[2:]
		} else {
			// If the string contains hex digits a-f, treat it as hex even without 0x.
			for i := 0; i < len(s); i++ {
				c := s[i]
				if (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
					base = 16
					break
				}
			}
		}
		v, err := strconv.ParseUint(s, base, 64)
		if err != nil {
			return fmt.Errorf("invalid uint64 string %q: %w", s, err)
		}
		*u = LenientUint64(v)
		return nil
	}

	// Fallback: JSON number
	var n uint64
	if err := json.Unmarshal(input, &n); err == nil {
		*u = LenientUint64(n)
		return nil
	}

	// Fallback: json.Number (covers large ints encoded as number)
	var num json.Number
	if err := json.Unmarshal(input, &num); err == nil {
		v, err := num.Int64()
		if err != nil {
			return err
		}
		if v < 0 {
			return fmt.Errorf("negative uint64: %d", v)
		}
		*u = LenientUint64(v)
		return nil
	}

	return fmt.Errorf("invalid uint64 json: %s", string(input))
}
