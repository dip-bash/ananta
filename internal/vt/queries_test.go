package vt

import (
	"bytes"
	"fmt"
	"testing"
)

func replies(input string) (string, Terminal) {
	var out bytes.Buffer
	term := New(WithSize(20, 5), WithWriter(&out))
	fmt.Fprint(term, input)
	return out.String(), term
}

func TestPrimaryDeviceAttributesAreAnswered(t *testing.T) {
	for _, q := range []string{"\x1b[c", "\x1b[0c"} {
		if got, _ := replies(q); got != "\x1b[?62;22c" {
			t.Errorf("%q answered %q", q, got)
		}
	}
}

func TestSecondaryDAAndVersionAreNotConfusedWithPrimary(t *testing.T) {
	if got, _ := replies("\x1b[>c"); got != "\x1b[>0;0;0c" {
		t.Errorf("secondary DA answered %q", got)
	}
	if got, _ := replies("\x1b[>0q"); got != "\x1bP>|ananta\x1b\\" {
		t.Errorf("version query answered %q", got)
	}
}

func TestExtensionSequencesAreIgnoredNotMisread(t *testing.T) {
	// ">4;1m" (key-encoding setup) must not act as SGR reset; "?u" and "=5u"
	// (keyboard-protocol query / enable) must not act as restore-cursor.
	out, term := replies("\x1b[1m\x1b[>4;1m\x1b[4;6H\x1b[?u\x1b[=5u\x1b[<u")
	if out != "" {
		t.Errorf("unexpected reply %q", out)
	}
	fmt.Fprint(term, "x") // lands where the cursor is, in whatever attributes are active
	term.Lock()
	defer term.Unlock()
	if g := term.Cell(5, 3); g.Char != 'x' {
		t.Errorf("cursor moved: 'x' not at (5,3) (a keyboard-protocol sequence acted as restore-cursor)")
	} else if g.Mode&4 == 0 {
		t.Errorf("bold lost after \">4;1m\" (misread as an SGR reset)")
	}
}

func TestDSRStillWorks(t *testing.T) {
	if got, _ := replies("\x1b[3;4H\x1b[6n"); got != "\x1b[3;4R" {
		t.Errorf("cursor position report %q", got)
	}
}
