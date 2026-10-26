// Package width measures terminal display width (cells) of text. Ambiguous
// characters (box drawing, arrows, ...) are always narrow, even on CJK locales,
// because that's what terminals do by default and our borders depend on it.
package width

import "github.com/mattn/go-runewidth"

var cond = func() *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = false
	c.StrictEmojiNeutral = true
	return c
}()

// Rune returns 0 (combining / zero-width), 1 or 2 cells.
func Rune(r rune) int {
	switch {
	case r >= 0xFE00 && r <= 0xFE0F, // variation selectors (emoji / text presentation)
		r >= 0x200B && r <= 0x200F, // zero-width space, joiners, direction marks
		r >= 0x2060 && r <= 0x2064,
		r >= 0xE0100 && r <= 0xE01EF:
		return 0
	}
	return cond.RuneWidth(r)
}

// String returns the display width of s.
func String(s string) int {
	n := 0
	for _, r := range s {
		n += Rune(r)
	}
	return n
}

// Truncate shortens s to at most n cells, ending with "…" when cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if String(s) <= n {
		return s
	}
	out, w := []rune(nil), 0
	for _, r := range s {
		rw := Rune(r)
		if w+rw > n-1 {
			break
		}
		out, w = append(out, r), w+rw
	}
	return string(out) + "…"
}
