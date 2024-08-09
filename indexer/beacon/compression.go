package beacon

import (
	"bytes"
	"compress/zlib"
)

// compressBytes compresses the given byte slice using zlib compression algorithm.
// It returns the compressed byte slice.
func compressBytes(data []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

// decompressBytes decompresses the given byte slice using zlib decompression algorithm.
// It returns the decompressed byte slice and any error encountered during decompression.
func decompressBytes(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		r.Close()
		return nil, err
	}

	buf := &bytes.Buffer{}
	buf.ReadFrom(r)
	r.Close()

	return buf.Bytes(), nil
}
