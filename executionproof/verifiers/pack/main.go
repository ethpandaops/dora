// Command pack prepares a built verifier module for embedding: the custom
// sections (names, producers, target features, wasm-bindgen metadata) are
// dropped, the runtime needs none of them, and the module is snappy-compressed.
package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/golang/snappy"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: pack <module.wasm> <module.wasm.sz>")
		os.Exit(1)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	module, err := stripCustomSections(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], snappy.Encode(nil, module), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// stripCustomSections returns the module without its custom sections (id 0).
func stripCustomSections(module []byte) ([]byte, error) {
	const headerSize = 8 // magic and version
	if len(module) < headerSize {
		return nil, fmt.Errorf("not a wasm module")
	}

	out := append(make([]byte, 0, len(module)), module[:headerSize]...)
	for pos := headerSize; pos < len(module); {
		id := module[pos]
		size, n := binary.Uvarint(module[pos+1:])
		if n <= 0 {
			return nil, fmt.Errorf("invalid section size at %d", pos)
		}
		end := pos + 1 + n + int(size)
		if end > len(module) {
			return nil, fmt.Errorf("section at %d exceeds the module", pos)
		}
		if id != 0 {
			out = append(out, module[pos:end]...)
		}
		pos = end
	}

	return out, nil
}
