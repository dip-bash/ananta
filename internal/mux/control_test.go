package mux

import "testing"

func TestParseWidth(t *testing.T) {
	good := map[string]float64{"0.33": 0.33, "33%": 0.33, "33": 0.33, "0.1": 0.1, "10%": 0.1, "1": 1, "100%": 1, " 50% ": 0.5}
	for in, want := range good {
		got, err := ParseWidth(in)
		if err != nil || got < want-1e-9 || got > want+1e-9 {
			t.Errorf("ParseWidth(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "0", "-5", "0%", "101", "150%", "1e9"} {
		if v, err := ParseWidth(in); err == nil {
			t.Errorf("ParseWidth(%q) = %v, want an error", in, v)
		}
	}
}
