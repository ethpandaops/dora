package utils

import (
	"bytes"
	"html/template"
	"math"
	"math/big"
	"os"
	"strings"
	"unicode/utf8"

	logger "github.com/sirupsen/logrus"
)

// GetTemplateFuncs will get the template functions
func GetTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"includeHTML": IncludeHTML,
		"bigIntCmp":   func(i *big.Int, j int) int { return i.Cmp(big.NewInt(int64(j))) },
		"mod":         func(i, j int) bool { return i%j == 0 },
		"sub":         func(i, j int) int { return i - j },
		"subUI64":     func(i, j uint64) uint64 { return i - j },
		"add":         func(i, j int) int { return i + j },
		"addI64":      func(i, j int64) int64 { return i + j },
		"addUI64":     func(i, j uint64) uint64 { return i + j },
		"addFloat64":  func(i, j float64) float64 { return i + j },
		"mul":         func(i, j float64) float64 { return i * j },
		"div":         func(i, j float64) float64 { return i / j },
		"divInt":      func(i, j int) float64 { return float64(i) / float64(j) },
		"nef":         func(i, j float64) bool { return i != j },
		"gtf":         func(i, j float64) bool { return i > j },
		"ltf":         func(i, j float64) bool { return i < j },
		"round": func(i float64, n int) float64 {
			return math.Round(i*math.Pow10(n)) / math.Pow10(n)
		},
		"percent":                    func(i float64) float64 { return i * 100 },
		"contains":                   strings.Contains,
		"formatAddCommas":            FormatAddCommas,
		"formatFloat":                FormatFloat,
		"formatBitlist":              FormatBitlist,
		"formatBitvectorValidators":  formatBitvectorValidators,
		"formatParticipation":        FormatParticipation,
		"formatEthFromGwei":          FormatETHFromGwei,
		"formatEthFromGweiShort":     FormatETHFromGweiShort,
		"formatFullEthFromGwei":      FormatFullETHFromGwei,
		"formatEthAddCommasFromGwei": FormatETHAddCommasFromGwei,
		"formatAmount":               FormatAmount,
		"ethBlockLink":               FormatEthBlockLink,
		"ethBlockHashLink":           FormatEthBlockHashLink,
		"ethAddressLink":             FormatEthAddressLink,
		"formatValidator":            FormatValidator,
		"formatValidatorWithIndex":   FormatValidatorWithIndex,
		"formatSlashedValidator":     FormatSlashedValidator,
		"formatRecentTimeShort":      FormatRecentTimeShort,
		"formatGraffiti":             FormatGraffiti,
	}
}

// IncludeHTML adds html to the page
func IncludeHTML(path string) template.HTML {
	b, err := os.ReadFile(path)
	if err != nil {
		logger.Printf("includeHTML - error reading file: %v", err)
		return ""
	}
	return template.HTML(string(b))
}

func GraffitiToString(graffiti []byte) string {
	s := strings.Map(fixUtf, string(bytes.Trim(graffiti, "\x00")))
	s = strings.Replace(s, "\u0000", "", -1) // rempove 0x00 bytes as it is not supported in postgres

	if !utf8.ValidString(s) {
		return "INVALID_UTF8_STRING"
	}

	return s
}

// FormatGraffitiString formats (and escapes) the graffiti
func FormatGraffitiString(graffiti string) string {
	return strings.Map(fixUtf, template.HTMLEscapeString(graffiti))
}

func fixUtf(r rune) rune {
	if r == utf8.RuneError {
		return -1
	}
	return r
}
