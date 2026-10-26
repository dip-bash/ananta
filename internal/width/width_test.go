package width

import "testing"

func TestWidths(t *testing.T) {
	cases := map[rune]int{'a': 1, '你': 2, 'ア': 2, '😀': 2, '│': 1, '─': 1, '█': 1, 0x0301: 0, 0xFE0F: 0, 'é': 1}
	for r, want := range cases {
		if got := Rune(r); got != want {
			t.Errorf("Rune(%q U+%04X) = %d, want %d", r, r, got, want)
		}
	}
	if String("a你b") != 4 {
		t.Error("String width")
	}
	if got := Truncate("你好世界", 5); got != "你好…" || String(got) > 5 {
		t.Errorf("Truncate = %q", got)
	}
}
