package utils

import (
	"bytes"
	"runtime"
	"strconv"
)

var (
	goroutinePrefix = []byte("goroutine ")
)

// This is terrible, slow, and should never be used.
func Goid() int {
	buf := make([]byte, 32)
	n := runtime.Stack(buf, false)
	buf = buf[:n]
	// goroutine 1 [running]: ...

	buf, ok := bytes.CutPrefix(buf, goroutinePrefix)
	if !ok {
		return 0
	}

	i := bytes.IndexByte(buf, ' ')
	if i < 0 {
		return 0
	}

	res, _ := strconv.Atoi(string(buf[:i]))
	return res
}
