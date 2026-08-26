package handlers

import "testing"

func TestNormalizeBlockRoot(t *testing.T) {
	valid := "0x" + "ab12" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab"
	tests := []struct{ in, want string }{
		{valid, valid},
		{"0xAB12" + "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789AB", valid},
		{"  " + valid + "  ", valid},
		{"", ""},
		{"0x1234", ""},                  // too short
		{valid[2:], ""},                 // missing 0x
		{"0x" + "zz12" + valid[6:], ""}, // not hex
		{valid + "00", ""},              // too long
	}

	for _, tt := range tests {
		if got := normalizeBlockRoot(tt.in); got != tt.want {
			t.Errorf("normalizeBlockRoot(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
