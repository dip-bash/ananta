package config

import (
	"strconv"
	"strings"

	"ananta/internal/screen"
)

// ParseColor accepts "#rrggbb", a 0-255 palette index, or "default".
func ParseColor(s string) screen.Col {
	s = strings.TrimSpace(s)
	if s == "" || s == "default" {
		return screen.ColDefault
	}
	if strings.HasPrefix(s, "#") && len(s) == 7 {
		if v, err := strconv.ParseUint(s[1:], 16, 32); err == nil {
			return screen.RGB(uint8(v>>16), uint8(v>>8), uint8(v))
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < 256 {
		return screen.Col(n)
	}
	return screen.ColDefault
}

// BorderSet holds the glyphs of one border style. Rounded corners are
// deliberately not offered.
type BorderSet struct{ H, V, TL, TR, BL, BR rune }

func BorderFor(style string) BorderSet {
	switch style {
	case "double":
		return BorderSet{'═', '║', '╔', '╗', '╚', '╝'}
	case "heavy":
		return BorderSet{'━', '┃', '┏', '┓', '┗', '┛'}
	case "ascii":
		return BorderSet{'-', '|', '+', '+', '+', '+'}
	}
	return BorderSet{'─', '│', '┌', '┐', '└', '┘'}
}
