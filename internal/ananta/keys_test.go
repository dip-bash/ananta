package ananta

import "testing"

func TestNormalizeChordSuper(t *testing.T) {
	if got := normalizeChord("super+h"); got != "super+h" {
		t.Fatalf("expected super+h, got %q", got)
	}
}

func TestParseSGRMouse(t *testing.T) {
	ks := parseKeys([]byte("\x1b[<0;10;5M"))
	if len(ks) != 1 || ks[0].Mouse == nil || !ks[0].Mouse.Left {
		t.Fatalf("expected left mouse event, got %#v", ks)
	}
	if ks[0].Mouse.X != 9 || ks[0].Mouse.Y != 4 {
		t.Fatalf("unexpected mouse coords: %#v", ks[0].Mouse)
	}

	ks = parseKeys([]byte("\x1b[<64;3;4M"))
	if len(ks) != 1 || ks[0].Mouse == nil || ks[0].Mouse.WheelDelta != -1 {
		t.Fatalf("expected wheel up event, got %#v", ks)
	}
}
