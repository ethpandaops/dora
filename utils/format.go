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
	"github.com/prysmaticlabs/go-bitfield"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/ethpandaops/dora/types"
)

func FormatETH(num string) string {
	floatNum, _ := strconv.ParseFloat(num, 64)
	return fmt.Sprintf("%.4f", floatNum/math.Pow10(18)) + " " + Config.Chain.TokenSymbol
}

func FormatETHFromGwei(gwei uint64) string {
	return fmt.Sprintf("%.4f", float64(gwei)/math.Pow10(9)) + " " + Config.Chain.TokenSymbol
}

func FormatETHFromGweiShort(gwei uint64) string {
	return fmt.Sprintf("%.4f", float64(gwei)/math.Pow10(9))
}

func FormatFullEthFromGwei(gwei uint64) string {
	return fmt.Sprintf("%v LYX", uint64(float64(gwei)/math.Pow10(9)))
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

func FormatBaseFee(weiValue uint64) template.HTML {
	// Convert wei to gwei (1 gwei = 1e9 wei)
	gweiValue := float64(weiValue) / 1e9

	// If less than 100,000 wei, show in wei
	if weiValue < 100000 {
		return template.HTML(string(FormatAddCommas(weiValue)) + " wei")
	}

	// If less than 100,000 gwei, show in gwei with 6 decimals, trimmed
	if gweiValue < 100000 {
		formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", gweiValue), "0"), ".")
		return template.HTML(formatted + " gwei")
	}

	// Show in LYX for very large values with 6 decimals, trimmed
	ethValue := gweiValue / 1e9
	formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", ethValue), "0"), ".")
	return template.HTML(formatted + " LYX")
}

func FormatBlobFeeDifference(eip7918Value, originalValue uint64) template.HTML {
	// Calculate the difference
	difference := eip7918Value - originalValue

	// Use the same formatting logic as FormatBaseFee for consistency
	return FormatBaseFee(difference)
}

func FormatTransactionValue(ethValue float64) template.HTML {
	// Convert LYX value to wei (1 LYX = 1e18 wei)
	weiValue := uint64(ethValue * 1e18)

	// Use the same formatting logic as FormatBaseFee
	return FormatBaseFee(weiValue)
}

func formatPercentageAlert(num float64, precision int, warnBelow float64, errBelow float64) template.HTML {
	p := message.NewPrinter(language.English)
	f := fmt.Sprintf("%%.%vf", precision)
	s := strings.TrimRight(strings.TrimRight(p.Sprintf(f, num), "0"), ".")
	r := []rune(p.Sprintf(s, num))
	switch {
	case num < errBelow:
		return template.HTML(fmt.Sprintf("<span class=\"text-danger\">%s%%</span>", string(r)))
	case num < warnBelow:
		return template.HTML(fmt.Sprintf("<span class=\"text-warning\">%s%%</span>", string(r)))
	default:
		return template.HTML(fmt.Sprintf("%s%%", string(r)))
	}
}

func FormatAddCommasFormatted(num float64, precision uint) template.HTML {
	p := message.NewPrinter(language.English)
	s := p.Sprintf(fmt.Sprintf("%%.%vf", precision), num)
	if precision > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return template.HTML(strings.ReplaceAll(string([]rune(p.Sprintf(s, num))), ",", `<span class="thousands-separator"></span>`))
}

func FormatBigNumberAddCommasFormatted(val hexutil.Big, precision uint) template.HTML {
	return FormatAddCommasFormatted(float64(val.ToInt().Int64()), 0)
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
	if unit == "LYX" || unit == "Ether" {
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

func FormatEthTransactionLink(hash []byte, width uint64) template.HTML {
	txhash := common.Hash(hash).String()
	caption := txhash
	if width > 0 {
		caption = caption[:width] + "…"
	}

	if Config.Frontend.EthExplorerLink != "" {
		link, err := url.JoinPath(Config.Frontend.EthExplorerLink, "tx", txhash)
		if err == nil {
			return template.HTML(fmt.Sprintf(`<a href="%v">%v</a>`, link, caption))
		}
	}
	return template.HTML(caption)
}

func FormatEthAddress(address []byte) template.HTML {
	caption := common.BytesToAddress(address).String()
	return template.HTML(caption)
}

func FormatValidator(index uint64, name string) template.HTML {
	return formatValidator(index, name, "fa-male mr-2", false)
}

func FormatValidatorWithIndex(index uint64, name string) template.HTML {
	return formatValidator(index, name, "fa-male mr-2", true)
}

func FormatSlashedValidator(index uint64, name string) template.HTML {
	return formatValidator(index, name, "fa-user-slash mr-2 text-danger", true)
}

func formatValidator(index uint64, name string, icon string, withIndex bool) template.HTML {
	if index == math.MaxInt64 {
		return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-index\"><i class=\"fas %v\"></i> unknown</span>", icon))
	} else if name != "" {
		var nameLabel string
		if withIndex {
			nameLabel = fmt.Sprintf("%v (%v)", html.EscapeString(name), index)
		} else {
			nameLabel = html.EscapeString(name)
		}
		return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-name\" data-bs-toggle=\"tooltip\" data-bs-placement=\"top\" data-bs-title=\"%v\"><i class=\"fas %v\"></i> <a href=\"/validator/%v\">%v</a></span>", index, icon, index, nameLabel))
	}
	return template.HTML(fmt.Sprintf("<span class=\"validator-label validator-index\"><i class=\"fas %v\"></i> <a href=\"/validator/%v\">%v</a></span>", icon, index, index))
}

func FormatValidatorNameWithIndex(index uint64, name string) template.HTML {
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

func formatWithdrawalHash(hash []byte) template.HTML {
	var colorClass string
	if hash[0] == 0x01 {
		colorClass = "text-success"
	} else if hash[0] == 0x02 {
		colorClass = "text-info"
	} else {
		colorClass = "text-warning"
	}

	return template.HTML(fmt.Sprintf("<span class=\"text-monospace %s\">%#x</span><span class=\"text-monospace\">%x…%x</span>", colorClass, hash[:1], hash[1:2], hash[len(hash)-2:]))
}

func FormatWithdawalCredentials(hash []byte) template.HTML {
	if len(hash) != 32 {
		return "INVALID CREDENTIALS"
	}

	if (hash[0] == 0x01 || hash[0] == 0x02) && Config.Frontend.EthExplorerLink != "" {
		link, err := url.JoinPath(Config.Frontend.EthExplorerLink, "address", fmt.Sprintf("0x%x", hash[12:]))
		if err == nil {
			return template.HTML(fmt.Sprintf(`<a href="%v">%v</a>`, link, formatWithdrawalHash(hash)))
		}
	}

	return formatWithdrawalHash(hash)
}

// FormatGweiValue formats a gas value in Gwei
func FormatGweiValue(val uint64) string {
	return FormatFloat(float64(val)/float64(1e9), 2) + " Gwei"
}

// CalculatePercentage calculates the percentage of a value from a total
func CalculatePercentage(value uint64, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}

// FormatByteAmount converts a byte count to a human-readable string with appropriate unit (B, kB, MB, GB)
func FormatByteAmount(bytes uint64) template.HTML {
	const unit = 1024
	if bytes < unit {
		return template.HTML(fmt.Sprintf("%d B", bytes))
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(bytes) / float64(div)
	return template.HTML(fmt.Sprintf("%.2f %ciB", value, "kMGTPE"[exp]))
}

func FormatRecvDelay(delay int32) template.HTML {
	if delay == 0 {
		return template.HTML("-")
	}
	return template.HTML(fmt.Sprintf("%.2f s", float64(delay)/1000))
}

func formatAlertNumber(displayText string, value float64, yellowThreshold float64, redThreshold float64) template.HTML {
	switch {
	case value >= redThreshold:
		return template.HTML(fmt.Sprintf("<span class=\"text-danger\">%s</span>", displayText))
	case value >= yellowThreshold:
		return template.HTML(fmt.Sprintf("<span class=\"text-warning\">%s</span>", displayText))
	default:
		return template.HTML(displayText)
	}
}
