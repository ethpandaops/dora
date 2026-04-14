package db

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

func appendDollarPlaceholders(sql *strings.Builder, start, count int, separator string) {
	for i := 0; i < count; i++ {
		if i > 0 {
			sql.WriteString(separator)
		}
		sql.WriteByte('$')
		sql.WriteString(strconv.Itoa(start + i))
	}
}

func byteSliceMapKey(value []byte) string {
	return hex.EncodeToString(value)
}

func appendUint64ListPlaceholders(sql *strings.Builder, args *[]any, values []uint64, separator string) {
	start := len(*args) + 1
	if len(values) == 0 {
		*args = append(*args, uint64(0))
		appendDollarPlaceholders(sql, start, 1, separator)
		return
	}
	for _, value := range values {
		*args = append(*args, value)
	}
	appendDollarPlaceholders(sql, start, len(values), separator)
}

func appendWithOrphanedFilter(sql *strings.Builder, args *[]any, filterOp *string, withOrphaned uint8, canonicalForkIds []uint64, column string) {
	if withOrphaned == 1 {
		return
	}

	switch withOrphaned {
	case 0:
		fmt.Fprintf(sql, " %v %s IN (", *filterOp, column)
		appendUint64ListPlaceholders(sql, args, canonicalForkIds, ",")
		fmt.Fprint(sql, ")")
		*filterOp = "AND"
	case 2:
		fmt.Fprintf(sql, " %v %s NOT IN (", *filterOp, column)
		appendUint64ListPlaceholders(sql, args, canonicalForkIds, ",")
		fmt.Fprint(sql, ")")
		*filterOp = "AND"
	}
}
