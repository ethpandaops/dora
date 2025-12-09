package utils

import (
	"net/url"
	"strconv"
	"strings"
)

func DecodeUint64BitfieldFromQuery(query string, fieldName string) uint64 {
	for query == "" {
		return 0
	}

	fieldValue := uint64(0)

	for query != "" {
		var key string
		key, query, _ = strings.Cut(query, "&")
		if strings.Contains(key, ";") {
			continue
		}
		if key == "" {
			continue
		}
		key, value, _ := strings.Cut(key, "=")
		if key != fieldName {
			continue
		}
		value, err1 := url.QueryUnescape(value)
		if err1 != nil {
			continue
		}

		if strings.HasPrefix(value, "0x") {
			value = value[2:]
			// parse to uint64
			val, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				continue
			}
			fieldValue |= val
		} else if strings.Contains(value, " ") {
			for _, val := range strings.Split(value, " ") {
				val, err := strconv.ParseUint(val, 10, 64)
				if err != nil {
					continue
				}
				fieldValue |= 1 << (val - 1)
			}
		} else {
			val, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				continue
			}
			fieldValue |= 1 << (val - 1)
		}

	}

	return fieldValue
}
