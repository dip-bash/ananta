package input

import (
	"sync"
	"testing"
	"time"
)

func names(ks []Key) []string {
	var out []string
	for _, k := range ks {
		out = append(out, k.Name)
	}
	return out
}

func TestParseLegacy(t *testing.T) {
	cases := map[string]string{
		"\x1bh":     "alt+h",
		"\x1bQ":     "alt+shift+q",
		"\x1b\r":    "alt+enter",
		"\x1b[1;3D": "alt+left",
		"\x1b[1;4C": "alt+shift+right",
		"\x1b[6~":   "pagedown",
		"\x03":      "ctrl+c",
		"a":         "a",
	}
	for in, want := range cases {
		got := ParseKeys([]byte(in))
		if len(got) != 1 || got[0].Name != want {
			t.Errorf("%q: got %v, want %s", in, names(got), want)
		}
	}
}

func TestParseCSIu(t *testing.T) {
	ks := ParseKeys([]byte("\x1b[104;9u")) // super+h
	if len(ks) != 1 || ks[0].Name != "super+h" || ks[0].Raw != nil {
		t.Fatalf("super+h: %+v", ks)
	}
	ks = ParseKeys([]byte("\x1b[99;5u")) // ctrl+c must be forwarded as 0x03
	if len(ks) != 1 || ks[0].Name != "ctrl+c" || string(ks[0].Raw) != "\x03" {
		t.Fatalf("ctrl+c: %+v", ks)
	}
}

func TestParseMouse(t *testing.T) {
	ks := ParseKeys([]byte("\x1b[<0;12;5M"))
	if len(ks) != 1 || ks[0].Mouse == nil || ks[0].Mouse.X != 11 || ks[0].Mouse.Y != 4 || !ks[0].Mouse.Press {
		t.Fatalf("mouse: %+v", ks)
	}
}

func TestNormalizeChord(t *testing.T) {
	cases := []struct {
		in     string
		leader int
		want   string
	}{
		{"leader+H", ModAlt, "alt+shift+h"},
		{"leader+left", ModSuper, "super+left"},
		{"Alt+Return", 0, "alt+enter"},
		{"leader++", ModCtrl, "ctrl++"},
	}
	for _, c := range cases {
		if got := NormalizeChord(c.in, c.leader); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestAssemblerHoldsSplitSequences(t *testing.T) {
	var got []Key
	a := NewAssembler(func(ks []Key) { got = append(got, ks...) })
	defer a.Close()
	// a mouse report and an alt+arrow, each cut in two by a read boundary,
	// and a UTF-8 character split mid-rune
	for _, chunk := range []string{"\x1b[<0;12", ";5M", "\x1b[1;", "3D", "\xe4\xbd", "\xa0"} {
		a.Feed([]byte(chunk))
	}
	if len(got) != 3 || got[0].Mouse == nil || got[0].Mouse.X != 11 || got[1].Name != "alt+left" || got[2].Name != "你" {
		t.Fatalf("split sequences were not reassembled: %+v", got)
	}
	for _, k := range got {
		if k.Mouse == nil && len(k.Raw) > 0 && k.Raw[0] == '[' {
			t.Fatalf("fragment leaked as text: %q", k.Raw)
		}
	}
}

func TestAssemblerLoneEscapeStillWorks(t *testing.T) {
	ch := make(chan []Key, 1)
	a := NewAssembler(func(ks []Key) { ch <- ks })
	defer a.Close()
	a.Feed([]byte{0x1b})
	select {
	case ks := <-ch:
		if len(ks) != 1 || ks[0].Name != "esc" {
			t.Fatalf("got %+v", ks)
		}
	case <-time.After(time.Second):
		t.Fatal("lone Escape was never delivered")
	}
}

func TestAssemblerWaitsForSlowLink(t *testing.T) {
	var got []Key
	var mu sync.Mutex
	a := NewAssembler(func(ks []Key) { mu.Lock(); got = append(got, ks...); mu.Unlock() })
	defer a.Close()
	a.Feed([]byte("\x1b[<0;1"))
	time.Sleep(150 * time.Millisecond) // a slow network, longer than a lone-Esc timeout
	a.Feed([]byte(";1M"))
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Mouse == nil {
		t.Fatalf("delayed remainder not joined: %+v", got)
	}
}
