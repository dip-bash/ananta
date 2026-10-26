package vt

import (
	"fmt"
	"testing"
)

func TestTabsAfterWiden(t *testing.T) {
	term := New(WithSize(20, 3))
	term.Resize(40, 3)
	fmt.Fprint(term, "\x1b[1;21H\tX") // go to col 21 (index 20), TAB, write X
	term.Lock()
	defer term.Unlock()
	for x := 0; x < 40; x++ {
		if term.Cell(x, 0).Char == 'X' {
			t.Logf("X landed at col %d (next default tab stop is 24)", x)
			if x != 24 {
				t.Fail()
			}
			return
		}
	}
	t.Fatal("X not found")
}
