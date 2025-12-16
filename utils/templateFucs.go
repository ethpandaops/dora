package utils

import (
	"bytes"
	"encoding/json"
	"html"
	"html/template"
	"math"
	"math/big"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Masterminds/sprig/v3"
	logger "github.com/sirupsen/logrus"
)

// GetTemplateFuncs will get the template functions
func GetTemplateFuncs() template.FuncMap {
	fm := template.FuncMap{}

	for k, v := range sprig.FuncMap() {
		fm[k] = v
	}

	customFuncs := template.FuncMap{
		"includeHTML": IncludeHTML,
		"includeJSON": IncludeJSON,
		"html":        func(x string) template.HTML { return template.HTML(x) },
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
		"inlist":      checkInList,
		"round": func(i float64, n int) float64 {
			return math.Round(i*math.Pow10(n)) / math.Pow10(n)
		},
		"uint64ToTime":                 func(i uint64) time.Time { return time.Unix(int64(i), 0).UTC() },
		"percent":                      func(i float64) float64 { return i * 100 },
		"contains":                     strings.Contains,
		"tokenSymbol":                  tokenSymbol,
		"formatAddCommas":              FormatAddCommas,
		"formatFloat":                  FormatFloat,
		"formatBaseFee":                FormatBaseFee,
		"formatBlobFeeDifference":      FormatBlobFeeDifference,
		"formatTransactionValue":       FormatTransactionValue,
		"formatBitlist":                FormatBitlist,
		"formatBitvectorValidators":    formatBitvectorValidators,
		"formatParticipation":          FormatParticipation,
		"formatEthFromGwei":            FormatETHFromGwei,
		"formatEthFromGweiShort":       FormatETHFromGweiShort,
		"formatFullEthFromGwei":        FormatFullEthFromGwei,
		"formatEthAddCommasFromGwei":   FormatETHAddCommasFromGwei,
		"formatBytesAmount":            FormatBytesAmount,
		"formatAmount":                 FormatAmount,
		"formatBigAmount":              FormatBigAmount,
		"formatAmountFormatted":        FormatAmountFormatted,
		"formatGwei":                   FormatGweiValue,
		"formatByteAmount":             FormatByteAmount,
		"percentage":                   CalculatePercentage,
		"ethBlockLink":                 FormatEthBlockLink,
		"ethBlockHashLink":             FormatEthBlockHashLink,
		"ethAddressLink":               FormatEthAddressLink,
		"ethTransactionLink":           FormatEthTransactionLink,
		"formatEthAddress":             FormatEthAddress,
		"formatValidator":              FormatValidator,
		"formatValidatorWithIndex":     FormatValidatorWithIndex,
		"formatValidatorNameWithIndex": FormatValidatorNameWithIndex,
		"formatSlashedValidator":       FormatSlashedValidator,
		"formatWithdawalCredentials":   FormatWithdawalCredentials,
		"formatRecentTimeShort":        FormatRecentTimeShort,
		"formatGraffiti":               FormatGraffiti,
		"formatRecvDelay":              FormatRecvDelay,
		"formatPercentageAlert":        formatPercentageAlert,
		"formatAlertNumber":            formatAlertNumber,
	}

	for k, v := range customFuncs {
		fm[k] = v
	}

	return fm
}

func checkInList(item, list string) bool {
	items := strings.Split(list, ",")
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
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

// IncludeJSON adds json to the page
func IncludeJSON(obj any, escapeHTML bool) template.HTML {
	b, err := json.Marshal(obj)
	if err != nil {
		logger.Printf("includeJSON - error marshalling json: %v", err)
		return ""
	}

	s := string(b)
	if escapeHTML {
		s = html.EscapeString(s)
	}
	return template.HTML(s)
}

func GraffitiToString(graffiti []byte) string {
	s := strings.Map(fixUtf, string(bytes.Trim(graffiti, "\x00")))
	s = strings.Replace(s, "\u0000", "", -1) // remove 0x00 bytes as it is not supported in postgres

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
