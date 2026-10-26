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
	r.Flush(0, 0, false)
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
