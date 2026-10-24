package vt

import (
	"fmt"
	"strings"
	"testing"
)

func rowText(t Terminal, abs int) string {
	var sb strings.Builder
	for _, g := range t.Row(abs) {
		if g.Char == 0 {
			g.Char = ' '
		}
		sb.WriteRune(g.Char)
	}
	return strings.TrimRight(sb.String(), " ")
}

func TestScrollbackKeepsScrolledLines(t *testing.T) {
	term := New(WithSize(20, 5))
	term.SetScrollbackLimit(100)
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(term, "line %d\r\n", i)
	}
	term.Lock()
	defer term.Unlock()
	n := term.ScrollbackLen()
	if n < 8 {
		t.Fatalf("expected >= 8 history lines, got %d", n)
	}
	if got := rowText(term, 0); got != "line 1" {
		t.Fatalf("oldest line = %q", got)
	}
	if got := rowText(term, n-1); got != fmt.Sprintf("line %d", n) {
		t.Fatalf("newest history line = %q (n=%d)", got, n)
	}
}

func TestScrollbackLimitAndAltScreen(t *testing.T) {
	term := New(WithSize(20, 4))
	term.SetScrollbackLimit(5)
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(term, "L%d\r\n", i)
	}
	term.Lock()
	if term.ScrollbackLen() != 5 {
		t.Fatalf("limit not enforced: %d", term.ScrollbackLen())
	}
	term.Unlock()
	fmt.Fprint(term, "\x1b[?1049h") // alt screen: output must not enter history
	for i := 0; i < 20; i++ {
		fmt.Fprintf(term, "alt %d\r\n", i)
	}
	term.Lock()
	defer term.Unlock()
	if term.ScrollbackLen() != 5 {
		t.Fatalf("alt screen leaked into history: %d", term.ScrollbackLen())
	}
}
