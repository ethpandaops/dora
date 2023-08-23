package utils

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/prysmaticlabs/go-bitfield"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func FormatETH(num string) string {
	floatNum, _ := strconv.ParseFloat(num, 64)
	return fmt.Sprintf("%.4f", floatNum/math.Pow10(18)) + " ETH"
}

func FormatETHFromGwei(gwei uint64) string {
	return fmt.Sprintf("%.4f", float64(gwei)/math.Pow10(9)) + " ETH"
}

func FormatETHFromGweiShort(gwei uint64) string {
	return fmt.Sprintf("%.4f", float64(gwei)/math.Pow10(9))
}

func FormatFullETHFromGwei(gwei uint64) string {
	return fmt.Sprintf("%v ETH", uint64(float64(gwei)/math.Pow10(9)))
}

func FormatETHAddCommasFromGwei(gwei uint64) template.HTML {
	return FormatAddCommas(uint64(float64(gwei) / math.Pow10(9)))
}

func FormatFloat(num float64, precision int) string {
	p := message.NewPrinter(language.English)
	f := fmt.Sprintf("%%.%vf", precision)
	s := strings.TrimRight(strings.TrimRight(p.Sprintf(f, num), "0"), ".")
	r := []rune(p.Sprintf(s, num))
	return string(r)
}

func FormatAddCommasFormated(num float64, precision uint) template.HTML {
	p := message.NewPrinter(language.English)
	s := p.Sprintf(fmt.Sprintf("%%.%vf", precision), num)
	if precision > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return template.HTML(strings.ReplaceAll(string([]rune(p.Sprintf(s, num))), ",", `<span class="thousands-separator"></span>`))
}

func FormatBigNumberAddCommasFormated(val hexutil.Big, precision uint) template.HTML {
	return FormatAddCommasFormated(float64(val.ToInt().Int64()), 0)
}

func FormatAddCommas(n uint64) template.HTML {
	number := FormatFloat(float64(n), 2)

	number = strings.ReplaceAll(number, ",", `<span class="thousands-separator"></span>`)
	return template.HTML(number)
}

func FormatBitlist(b []byte, v []types.NamedValidator) template.HTML {
	p := bitfield.Bitlist(b)
	return formatBits(p.BytesNoTrim(), int(p.Len()), v)
}

func formatBits(b []byte, length int, v []types.NamedValidator) template.HTML {
	var buf strings.Builder
	buf.WriteString("<div class=\"text-bitfield text-monospace\">")
	perLine := 8
	for y := 0; y < len(b); y += perLine {
		start, end := y*8, (y+perLine)*8
		if end >= length {
			end = length
		}
		for x := start; x < end; x++ {
			if x%8 == 0 {
				if x != 0 {
					buf.WriteString("</span> ")
				}
				buf.WriteString("<span>")
			}
			if v != nil && x < len(v) {
				val := v[x]
				if val.Name != "" {
					buf.WriteString(fmt.Sprintf(`<span data-bs-toggle="tooltip" data-bs-placement="top" title="%v (%v)">`, val.Name, val.Index))
				} else {
					buf.WriteString(fmt.Sprintf(`<span data-bs-toggle="tooltip" data-bs-placement="top" title="%v">`, val.Index))
				}
			}
			bit := BitAtVector(b, x)
			if bit {
				buf.WriteString("1")
			} else {
				buf.WriteString("0")
			}
			if v != nil && x < len(v) {
				buf.WriteString("</span>")
			}
		}
		buf.WriteString("</span><br/>")
	}
	buf.WriteString("</div>")
	return template.HTML(buf.String())
}

func formatBitvectorValidators(bits []byte, validators []types.NamedValidator) template.HTML {
	invalidLen := false
	if len(bits)*8 != len(validators) {
		invalidLen = true
	}
	var buf strings.Builder
	buf.WriteString("<pre class=\"text-monospace\" style=\"font-size:1rem;\">")
	for i := 0; i < len(bits)*8; i++ {
		if invalidLen {
			if BitAtVector(bits, i) {
				buf.WriteString("1")
			} else {
				buf.WriteString("0")
			}
		} else {
			val := validators[i]
			if val.Name != "" {
				buf.WriteString(fmt.Sprintf(`<span data-bs-toggle="tooltip" data-bs-placement="top" title="%v (%v)">`, val.Name, val.Index))
			} else {
				buf.WriteString(fmt.Sprintf(`<span data-bs-toggle="tooltip" data-bs-placement="top" title="%v">`, val.Index))
			}
			if BitAtVector(bits, i) {
				buf.WriteString("1")
			} else {
				buf.WriteString("0")
			}
			buf.WriteString("</span>")
		}

		if (i+1)%64 == 0 {
			buf.WriteString("\n")
		} else if (i+1)%8 == 0 {
			buf.WriteString(" ")
		}
	}
	buf.WriteString("</pre>")
	return template.HTML(buf.String())
}

func FormatParticipation(v float64) template.HTML {
	return template.HTML(fmt.Sprintf("<span>%.2f %%</span>", v*100.0))
}

func FormatAmountFormatted(amount *big.Int, unit string, digits int, maxPreCommaDigitsBeforeTrim int, fullAmountTooltip bool, smallUnit bool, newLineForUnit bool) template.HTML {
	return formatAmount(amount, unit, digits, maxPreCommaDigitsBeforeTrim, fullAmountTooltip, smallUnit, newLineForUnit)
}
func FormatAmount(amount *big.Int, unit string, digits int) template.HTML {
	return formatAmount(amount, unit, digits, 0, true, false, false)
}
func FormatBigAmount(amount *hexutil.Big, unit string, digits int) template.HTML {
	return FormatAmount((*big.Int)(amount), unit, digits)
}
func FormatBytesAmount(amount []byte, unit string, digits int) template.HTML {
	return FormatAmount(new(big.Int).SetBytes(amount), unit, digits)
}
func formatAmount(amount *big.Int, unit string, digits int, maxPreCommaDigitsBeforeTrim int, fullAmountTooltip bool, smallUnit bool, newLineForUnit bool) template.HTML {
	// define display unit & digits used per unit max
	displayUnit := " " + unit
	var unitDigits int
	if unit == "ETH" || unit == "Ether" {
		unitDigits = 18
	} else if unit == "GWei" {
		unitDigits = 9
	} else {
		displayUnit = " ?"
		unitDigits = 0
	}

	// small unit & new line for unit handling
	{
		unit = displayUnit
		if newLineForUnit {
			displayUnit = "<BR />"
		} else {
			displayUnit = ""
		}
		if smallUnit {
			displayUnit += `<span style="font-size: .63rem;`
			if newLineForUnit {
				displayUnit += `color: grey;`
			}
			displayUnit += `">` + unit + `</span>`
		} else {
			displayUnit += unit
		}
	}

	trimmedAmount, fullAmount := trimAmount(amount, unitDigits, maxPreCommaDigitsBeforeTrim, digits, false)
	tooltip := ""
	if fullAmountTooltip {
		tooltip = fmt.Sprintf(` data-bs-toggle="tooltip" data-bs-placement="top" title="%s"`, fullAmount)
	}

	// done, convert to HTML & return
	return template.HTML(fmt.Sprintf("<span%s>%s%s</span>", tooltip, trimmedAmount, displayUnit))
}

func trimAmount(amount *big.Int, unitDigits int, maxPreCommaDigitsBeforeTrim int, digits int, addPositiveSign bool) (trimmedAmount, fullAmount string) {
	// Initialize trimmedAmount and postComma variables to "0"
	trimmedAmount = "0"
	postComma := "0"
	proceed := ""

	if amount != nil {
		s := amount.String()
		if amount.Sign() > 0 && addPositiveSign {
			proceed = "+"
		} else if amount.Sign() < 0 {
			proceed = "-"
			s = strings.Replace(s, "-", "", 1)
		}
		l := len(s)

		// Check if there is a part of the amount before the decimal point
		if l > int(unitDigits) {
			// Calculate length of preComma part
			l -= unitDigits
			// Set preComma to part of the string before the decimal point
			trimmedAmount = s[:l]
			// Set postComma to part of the string after the decimal point, after removing trailing zeros
			postComma = strings.TrimRight(s[l:], "0")

			// Check if the preComma part exceeds the maximum number of digits before the decimal point
			if maxPreCommaDigitsBeforeTrim > 0 && l > maxPreCommaDigitsBeforeTrim {
				// Reduce the number of digits after the decimal point by the excess number of digits in the preComma part
				l -= maxPreCommaDigitsBeforeTrim
				if digits < l {
					digits = 0
				} else {
					digits -= l
				}
			}
			// Check if there is only a part of the amount after the decimal point, and no leading zeros need to be added
		} else if l == unitDigits {
			// Set postComma to part of the string after the decimal point, after removing trailing zeros
			postComma = strings.TrimRight(s, "0")
			// Check if there is only a part of the amount after the decimal point, and leading zeros need to be added
		} else if l != 0 {
			// Use fmt package to add leading zeros to the string
			d := fmt.Sprintf("%%0%dd", unitDigits-l)
			// Set postComma to resulting string, after removing trailing zeros
			postComma = strings.TrimRight(fmt.Sprintf(d, 0)+s, "0")
		}

		fullAmount = trimmedAmount
		if len(postComma) > 0 {
			fullAmount += "." + postComma
		}

		// limit floating part
		if len(postComma) > digits {
			postComma = postComma[:digits]
		}

		// set floating point
		if len(postComma) > 0 {
			trimmedAmount += "." + postComma
		}
	}
	return proceed + trimmedAmount, proceed + fullAmount
}

func FormatEthBlockLink(blockNum uint64) template.HTML {
	caption := FormatAddCommas(blockNum)
	if Config.Frontend.EthExplorerLink != "" {
		link, err := url.JoinPath(Config.Frontend.EthExplorerLink, "block", strconv.FormatUint(uint64(blockNum), 10))
		if err == nil {
			return template.HTML(fmt.Sprintf(`<a href="%v">%v</a>`, link, caption))
		}
	}
	return caption
}

func FormatEthBlockHashLink(blockHash []byte) template.HTML {
	caption := fmt.Sprintf("0x%x", blockHash)
	if Config.Frontend.EthExplorerLink != "" {
		link, err := url.JoinPath(Config.Frontend.EthExplorerLink, "block", caption)
		if err == nil {
			return template.HTML(fmt.Sprintf(`<a href="%v">%v</a>`, link, caption))
		}
	}
	return template.HTML(caption)
}

func FormatEthAddressLink(address []byte) template.HTML {
	caption := common.BytesToAddress(address).String()
	if Config.Frontend.EthExplorerLink != "" {
		link, err := url.JoinPath(Config.Frontend.EthExplorerLink, "address", caption)
		if err == nil {
			return template.HTML(fmt.Sprintf(`<a href="%v">%v</a>`, link, caption))
		}
	}
	return template.HTML(caption)
}

func FormatValidator(index uint64, name string) template.HTML {
	return formatValidator(index, name, "fa-male mr-2")
}

func FormatSlashedValidator(index uint64, name string) template.HTML {
	return formatValidator(index, name, "fa-user-slash mr-2 text-danger")
}

func formatValidator(index uint64, name string, icon string) template.HTML {
	if name != "" {
		return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-name\" data-bs-toggle=\"tooltip\" data-bs-placement=\"top\" data-bs-title=\"%v\"><i class=\"fas %v\"></i> <a href=\"/validator/%v\">%v</a></span>", index, icon, index, html.EscapeString(name)))
	}
	return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-index\"><i class=\"fas %v\"></i> <a href=\"/validator/%v\">%v</a></span>", icon, index, index))
}

func FormatValidatorWithIndex(index uint64, name string) template.HTML {
	if name != "" {
		return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-name\">%v (%v)</span>", html.EscapeString(name), index))
	}
	return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-index\">%v</span>", index))
}

func FormatRecentTimeShort(ts time.Time) template.HTML {
	duration := time.Until(ts)
	var timeStr string
	absDuraction := duration.Abs()
	if absDuraction < 1*time.Second {
		return template.HTML("now")
	} else if absDuraction < 60*time.Second {
		timeStr = fmt.Sprintf("%v sec.", uint(absDuraction.Seconds()))
	} else if absDuraction < 60*time.Minute {
		timeStr = fmt.Sprintf("%v min.", uint(absDuraction.Minutes()))
	} else if absDuraction < 24*time.Hour {
		timeStr = fmt.Sprintf("%v hr.", uint(absDuraction.Hours()))
	} else {
		timeStr = fmt.Sprintf("%v day.", uint(absDuraction.Hours()/24))
	}
	if duration < 0 {
		return template.HTML(fmt.Sprintf("%v ago", timeStr))
	} else {
		return template.HTML(fmt.Sprintf("in %v", timeStr))
	}
}

func FormatGraffiti(graffiti []byte) template.HTML {
	return template.HTML(fmt.Sprintf("<span class=\"graffiti-label\" data-graffiti=\"%#x\">%s</span>", graffiti, html.EscapeString(string(graffiti))))
}
