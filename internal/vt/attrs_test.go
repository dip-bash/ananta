package vt

import (
	"bytes"
	"fmt"
	"testing"
)

func cellAfter(t *testing.T, input string) Glyph {
	t.Helper()
	term := New(WithSize(10, 2))
	fmt.Fprint(term, input+"x")
	term.Lock()
	defer term.Unlock()
	return term.Cell(0, 0)
}

func TestCursorStyle(t *testing.T) {
	term := New(WithSize(10, 2))
	for seq, want := range map[string]int{"\x1b[6 q": 6, "\x1b[2 q": 2, "\x1b[4 q": 4, "\x1b[0 q": 0, "\x1b[ q": 0, "\x1b[9 q": 6} {
		fmt.Fprint(term, seq)
		if got := term.CursorStyle(); got != want {
			t.Errorf("%q: cursor style %d, want %d", seq, got, want)
		}
	}
	fmt.Fprint(term, "\x1b[5 q")
	fmt.Fprint(term, "\x1bc") // full reset
	if term.CursorStyle() != 0 {
		t.Error("reset should restore the default cursor style")
	}
}

func TestFaintAndStrikethrough(t *testing.T) {
	if g := cellAfter(t, "\x1b[2m"); g.Mode&GlyphFaint == 0 {
		t.Error("SGR 2 (faint) ignored")
	}
	if g := cellAfter(t, "\x1b[9m"); g.Mode&GlyphStrike == 0 {
		t.Error("SGR 9 (strikethrough) ignored")
	}
	if g := cellAfter(t, "\x1b[1;2m\x1b[22m"); g.Mode&(GlyphBold|GlyphFaint) != 0 {
		t.Error("SGR 22 must clear both bold and faint")
	}
	if g := cellAfter(t, "\x1b[9m\x1b[29m"); g.Mode&GlyphStrike != 0 {
		t.Error("SGR 29 must clear strikethrough")
	}
	if g := cellAfter(t, "\x1b[2;9;1m\x1b[0m"); g.Mode&(GlyphFaint|GlyphStrike|GlyphBold) != 0 {
		t.Error("SGR 0 must clear everything")
	}
}

func TestColonSubparametersAreNotAReset(t *testing.T) {
	// curly underline (used for diagnostics) must be an underline, not "reset all"
	g := cellAfter(t, "\x1b[1m\x1b[38;5;196m\x1b[4:3m")
	if g.Mode&GlyphUnderline == 0 || g.Mode&GlyphBold == 0 || g.FG != 196 {
		t.Errorf("4:3 lost attributes: mode %d fg %d", g.Mode, g.FG)
	}
	if g := cellAfter(t, "\x1b[4m\x1b[4:0m"); g.Mode&GlyphUnderline != 0 {
		t.Error("4:0 must turn the underline off")
	}
	// colon form of a true colour, with and without the colour-space field
	for _, seq := range []string{"\x1b[38:2::10:20:30m", "\x1b[38:2:10:20:30m", "\x1b[38;2;10;20;30m"} {
		if g := cellAfter(t, seq); g.FG != Color(10<<16|20<<8|30) {
			t.Errorf("%q: fg %#x", seq, uint32(g.FG))
		}
	}
	// underline colour is accepted and ignored without disturbing the rest
	if g := cellAfter(t, "\x1b[1;58:2::1:2:3;3m"); g.Mode&GlyphBold == 0 || g.Mode&GlyphItalic == 0 {
		t.Errorf("underline colour broke following attributes: mode %d", g.Mode)
	}
}

func TestColourQueriesReportTheRealTerminalColours(t *testing.T) {
	var out bytes.Buffer
	term := New(WithSize(10, 2), WithWriter(&out))
	term.SetDefaultColors([3]uint8{0x10, 0x20, 0x30}, [3]uint8{0xfa, 0xfb, 0xfc}) // a light theme
	fmt.Fprint(term, "\x1b]10;?\x07\x1b]11;?\x07")
	if got, want := out.String(), "\x1b]10;rgb:1010/2020/3030\x07\x1b]11;rgb:fafa/fbfb/fcfc\x07"; got != want {
		t.Errorf("replied %q, want %q", got, want)
	}
}

func TestColourQueryFallbackIsSaneAndPaletteQueryIsNotInvented(t *testing.T) {
	var out bytes.Buffer
	term := New(WithSize(10, 2), WithWriter(&out))
	fmt.Fprint(term, "\x1b]11;?\x07")
	if out.String() != "\x1b]11;rgb:0000/0000/0000\x07" { // unknown terminal: assume black, not garbage
		t.Errorf("fallback background %q", out.String())
	}
	out.Reset()
	fmt.Fprint(term, "\x1b]4;5;?\x07")
	if out.String() != "" {
		t.Errorf("palette entry 5 is the user's theme colour; must not be invented: %q", out.String())
	}
}
