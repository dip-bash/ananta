package screen

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func frame(t *testing.T, build func(s *Screen)) string {
	t.Helper()
	var buf bytes.Buffer
	r := NewRenderer(bufio.NewWriter(&buf))
	r.Resize(10, 2)
	build(r.Cur())
	r.Flush(0, 0, false, 0)
	return buf.String()
}

func TestWideTextTakesTwoColumns(t *testing.T) {
	var next int
	out := frame(t, func(s *Screen) { next = s.Text(0, 0, "a你b", ColDefault, ColDefault, 0) })
	if next != 4 {
		t.Fatalf("Text returned column %d, want 4", next)
	}
	if !strings.Contains(out, "a你b") {
		t.Fatalf("expected contiguous output, got %q", out)
	}
	if strings.Count(out, "\x1b[1;") != 1 {
		t.Fatalf("one cursor move expected for a contiguous run: %q", out)
	}
}

func TestCutWideCharIsBlanked(t *testing.T) {
	out := frame(t, func(s *Screen) {
		s.Text(9, 0, "你", ColDefault, ColDefault, 0) // lead lands in the last column
		s.Text(0, 1, "你", ColDefault, ColDefault, 0)
		s.Set(1, 1, Cell{R: '│', FG: ColDefault, BG: ColDefault}) // border cuts the continuation
	})
	if strings.Contains(out, "你") {
		t.Fatalf("a cut wide character must not be sent to the terminal: %q", out)
	}
}

func TestClearIsInsideSyncUpdate(t *testing.T) {
	out := frame(t, func(s *Screen) {})
	i, j := strings.Index(out, "\x1b[?2026h"), strings.Index(out, "\x1b[2J")
	if i < 0 || j < i {
		t.Fatalf("screen clear must come after sync-begin: %q", out)
	}
}

func TestBasicColoursUseClassicCodes(t *testing.T) {
	out := frame(t, func(s *Screen) {
		s.Set(0, 0, Cell{R: 'a', FG: 1, BG: ColDefault})   // red
		s.Set(1, 0, Cell{R: 'b', FG: 12, BG: 4})           // bright blue on blue
		s.Set(2, 0, Cell{R: 'c', FG: 200, BG: 15})         // 256-palette fg, bright white bg
		s.Set(3, 0, Cell{R: 'd', FG: RGB(1, 2, 3), BG: 7}) // truecolor fg, white bg
	})
	for _, want := range []string{"\x1b[0;31ma", ";94;44mb", ";38;5;200;107mc", ";38;2;1;2;3;47md"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in %q", want, out)
		}
	}
	if strings.Contains(out, "38;5;1;") || strings.Contains(out, "38;5;12") {
		t.Errorf("basic colours must not use the 256-colour form: %q", out)
	}
}

func TestStrikeFaintBlinkAreSent(t *testing.T) {
	out := frame(t, func(s *Screen) {
		s.Set(0, 0, Cell{R: 'x', FG: ColDefault, BG: ColDefault, A: AttrDim | AttrStrike | AttrBlink})
	})
	if !strings.Contains(out, "\x1b[0;2;9;5mx") {
		t.Errorf("attributes missing: %q", out)
	}
}

func TestCursorStyleIsSentOnlyWhenItChanges(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(bufio.NewWriter(&buf))
	r.Resize(10, 2)
	count := func(s string) int { return strings.Count(buf.String(), s) }
	r.Flush(1, 1, true, 6)
	r.Flush(1, 1, true, 6)
	if count("\x1b[6 q") != 1 {
		t.Fatalf("same style sent twice: %q", buf.String())
	}
	r.Flush(1, 1, true, 2)
	if count("\x1b[2 q") != 1 {
		t.Fatalf("style change not sent: %q", buf.String())
	}
	r.Resize(10, 2) // a new client: it knows nothing yet
	r.Flush(1, 1, true, 2)
	if count("\x1b[2 q") != 2 {
		t.Fatalf("style not re-sent after a resize / re-attach: %q", buf.String())
	}
}
