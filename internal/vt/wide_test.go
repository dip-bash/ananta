package vt

import (
	"fmt"
	"testing"
)

func TestWideCharsOccupyTwoCells(t *testing.T) {
	term := New(WithSize(10, 3))
	fmt.Fprint(term, "a你b😀c")
	term.Lock()
	defer term.Unlock()
	want := []struct {
		x    int
		char rune
		mode int16
	}{{0, 'a', 0}, {1, '你', GlyphWide}, {2, 0, GlyphCont}, {3, 'b', 0}, {4, '😀', GlyphWide}, {5, 0, GlyphCont}, {6, 'c', 0}}
	for _, w := range want {
		g := term.Cell(w.x, 0)
		if g.Char != w.char || g.Mode&(GlyphWide|GlyphCont) != w.mode {
			t.Errorf("col %d: got %q mode %d, want %q mode %d", w.x, g.Char, g.Mode&(GlyphWide|GlyphCont), w.char, w.mode)
		}
	}
	if c := term.Cursor(); c.X != 7 {
		t.Errorf("cursor at %d, want 7", c.X)
	}
}

func TestWideCharWrapsInsteadOfSplitting(t *testing.T) {
	term := New(WithSize(5, 3))
	fmt.Fprint(term, "abcd你") // only one column left: must wrap to the next row
	term.Lock()
	defer term.Unlock()
	if g := term.Cell(4, 0); g.Char != ' ' {
		t.Errorf("last column should be blank, got %q", g.Char)
	}
	if g := term.Cell(0, 1); g.Char != '你' || g.Mode&GlyphWide == 0 {
		t.Errorf("wide char should start the next row, got %q", g.Char)
	}
}

func TestOverwritingHalfAWideCharBlanksTheRest(t *testing.T) {
	term := New(WithSize(10, 2))
	fmt.Fprint(term, "你好\x1b[1;2Hx") // overwrite the 2nd half of 你
	term.Lock()
	defer term.Unlock()
	if g := term.Cell(0, 0); g.Char != ' ' || g.Mode&GlyphWide != 0 {
		t.Errorf("orphaned first half not cleared: %q mode %d", g.Char, g.Mode)
	}
	if g := term.Cell(1, 0); g.Char != 'x' {
		t.Errorf("x not written: %q", g.Char)
	}
}

func TestInsertMode(t *testing.T) {
	term := New(WithSize(10, 2))
	fmt.Fprint(term, "abcd\x1b[1;2H\x1b[4hXY\x1b[4l")
	term.Lock()
	defer term.Unlock()
	got := ""
	for x := 0; x < 6; x++ {
		got += string(term.Cell(x, 0).Char)
	}
	if got != "aXYbcd" {
		t.Errorf("insert mode: %q, want aXYbcd", got)
	}
}

func TestOSC52Clipboard(t *testing.T) {
	term := New(WithSize(10, 2))
	var got string
	term.SetClipboardHandler(func(s string) { got = s })
	fmt.Fprint(term, "\x1b]52;c;aGVsbG8=\x07") // "hello"
	if got != "hello" {
		t.Errorf("clipboard handler got %q", got)
	}
}
