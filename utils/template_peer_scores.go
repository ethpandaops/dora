package utils

import (
	"encoding/json"
	"fmt"
	"html/template"
)

// peerScoreColor maps a normalized peer score in [-1, +1] to an HSL CSS
// color. Healthy peers (close to +1) render green; banned peers
// (close to -1) render red. The intermediate band fades through
// yellow so a quick scan of the matrix surfaces unhealthy cells.
func peerScoreColor(normalized any) template.CSS {
	v := toScoreFloat(normalized)
	if v > 1 {
		v = 1
	}
	if v < -1 {
		v = -1
	}

	// Map [-1, +1] linearly to hue [0, 120] (red → yellow → green).
	hue := (v + 1) * 60
	// Keep cells legible: high saturation, mid lightness.
	return template.CSS(fmt.Sprintf("hsl(%.0f, 65%%, 42%%)", hue))
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
