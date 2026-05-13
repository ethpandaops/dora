package utils

import (
	"encoding/json"
	"fmt"
	"html/template"
)

// peerScoreColor maps a normalized peer score to a discrete background
// color keyed on the same threshold bands as scoreStateFor (-0.2 =
// disconnect, -0.5 = ban). Discrete bands prevent the matrix from
// rendering four different greens for four "healthy" peers across
// clients with wildly different score scales. The intermediate
// disconnect band uses a darker orange so a quick scan immediately
// surfaces the unhealthy cells without distracting cross-cell shade
// differences.
func peerScoreColor(normalized any) template.CSS {
	v := toScoreFloat(normalized)
	switch {
	case v <= -0.5:
		return template.CSS("hsl(0, 60%, 38%)")
	case v <= -0.2:
		return template.CSS("hsl(28, 70%, 40%)")
	default:
		return template.CSS("hsl(120, 45%, 32%)")
	}
}

// peerScoreJSON marshals a value to a JSON string suitable for embedding
// in an HTML data-* attribute. Errors surface as "{}" so the page
// never breaks on a single bad component map.
func peerScoreJSON(value any) template.JS {
	b, err := json.Marshal(value)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(string(b))
}

func toScoreFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}
