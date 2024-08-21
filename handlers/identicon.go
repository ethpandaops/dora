package handlers

import (
	"crypto/md5"
	"math/big"
	"net/http"

	"github.com/lucasb-eyer/go-colorful"
	"github.com/stdatiks/jdenticon-go"
)

func generateIdenticonSVG(s string) []byte {
	c := jdenticon.DefaultConfig
	c.Background = generateColorFromHash(s)
	c.Width = 30
	c.Height = 30
	icon := jdenticon.NewWithConfig(s, c)
	svg, _ := icon.SVG()
	return svg
}

func generateColorFromHash(s string) colorful.Color {
	hash := md5.Sum([]byte(s))
	hashInt := new(big.Int).SetBytes(hash[:])
	hue := float64(hashInt.Int64() % 360)
	saturation := 1.0
	brightness := 1.0

	color := colorful.Hsv(hue, saturation, brightness)
	return color
}

func Identicon(w http.ResponseWriter, r *http.Request) {
	keys, ok := r.URL.Query()["key"]
	if !ok || len(keys[0]) < 1 {
		http.Error(w, "Missing key parameter", http.StatusBadRequest)
		return
	}
	key := keys[0]

	svg := generateIdenticonSVG(key[len(key)/2:])

	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	w.Write(svg)
}
