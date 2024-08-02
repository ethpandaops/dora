package beacon

import (
	"bytes"
	"compress/zlib"
)

func compressBytes(data []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Bytes()
}

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
