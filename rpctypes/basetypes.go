package rpctypes

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

type BytesHexStr []byte

func (s *BytesHexStr) UnmarshalText(b []byte) error {
	if s == nil {
		return fmt.Errorf("cannot unmarshal bytes into nil")
	}
	if len(b) >= 2 && b[0] == '0' && b[1] == 'x' {
		b = b[2:]
	}
	out := make([]byte, len(b)/2)
	hex.Decode(out, b)
	*s = out
	return nil
}

func (s *BytesHexStr) UnmarshalJSON(b []byte) error {
	if s == nil {
		return fmt.Errorf("cannot unmarshal bytes into nil")
	}
	var bytes []byte
	var tmpStr string
	if err := json.Unmarshal(b, &tmpStr); err == nil {
		if len(tmpStr) >= 2 && tmpStr[0] == '0' && tmpStr[1] == 'x' {
			tmpStr = tmpStr[2:]
		}
		bytes = make([]byte, len(tmpStr)/2)
		hex.Decode(bytes, []byte(tmpStr))
	} else {
		err := json.Unmarshal(b, &bytes)
		if err != nil {
			var tmpStrArr []string
			err = json.Unmarshal(b, &tmpStrArr)
			if err == nil {
				bytes = make([]byte, len(tmpStrArr))
				for idx, str := range tmpStrArr {
					n, e := strconv.ParseUint(str, 0, 64)
					if e != nil {
						err = e
						break
					}
					bytes[idx] = uint8(n)
				}
			}
		}
		if err != nil {
			fmt.Printf("err: %v\n", err)
			return err
		}
	}
	*s = bytes
	return nil
}

func (s *BytesHexStr) MarshalJSON() ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	return []byte(fmt.Sprintf("\"0x%x\"", []byte(*s))), nil
}

func (s BytesHexStr) String() string {
	return fmt.Sprintf("0x%x", []byte(s))
}

type Uint64Str uint64

func (s *Uint64Str) UnmarshalJSON(b []byte) error {
	return Uint64Unmarshal((*uint64)(s), b)
}

// Parse a uint64, with or without quotes, in any base, with common prefixes accepted to change base.
func Uint64Unmarshal(v *uint64, b []byte) error {
	if v == nil {
		return errors.New("nil dest in uint64 decoding")
	}
	if len(b) == 0 {
		return errors.New("empty uint64 input")
	}
	if b[0] == '"' || b[0] == '\'' {
		if len(b) == 1 || b[len(b)-1] != b[0] {
			return errors.New("uneven/missing quotes")
		}
		b = b[1 : len(b)-1]
	}
	n, err := strconv.ParseUint(string(b), 0, 64)
	if err != nil {
		return err
	}
	*v = n
	return nil
}
