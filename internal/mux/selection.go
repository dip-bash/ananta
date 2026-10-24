package mux

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"ananta/internal/clipboard"
	"ananta/internal/pane"
)

// Selection is a highlighted region of one pane in logical coordinates
// (history rows first, see scrollback.go). Kind: 'c' character stream, 'l' whole lines, 'b' block.
type Selection struct {
	P              *pane.Pane
	AX, AY, CX, CY int // anchor and cursor
	Kind           byte
}

// ordered returns the endpoints in reading order.
func (s *Selection) ordered() (sx, sy, ex, ey int) {
	if s.AY < s.CY || (s.AY == s.CY && s.AX <= s.CX) {
		return s.AX, s.AY, s.CX, s.CY
	}
	return s.CX, s.CY, s.AX, s.AY
}

func (s *Selection) Contains(x, y int) bool {
	sx, sy, ex, ey := s.ordered()
	if y < sy || y > ey {
		return false
	}
	switch s.Kind {
	case 'l':
		return true
	case 'b':
		return x >= min(s.AX, s.CX) && x <= max(s.AX, s.CX)
	}
	if y == sy && x < sx {
		return false
	}
	if y == ey && x > ex {
		return false
	}
	return true
}

// grid gives lazy, cached access to a pane's rows (history + screen) as
// fixed-width rune slices, in logical coordinates.
type grid struct {
	p     *pane.Pane
	v     paneView
	cols  int
	total int
	cache map[int][]rune
}

func newGrid(p *pane.Pane) *grid {
	v := viewOf(p)
	return &grid{p: p, v: v, cols: max(1, v.cols), total: v.total(), cache: map[int][]rune{}}
}

func (g *grid) row(y int) []rune {
	y = max(0, min(g.total-1, y))
	if r, ok := g.cache[y]; ok {
		return r
	}
	out := make([]rune, g.cols)
	for i := range out {
		out[i] = ' '
	}
	g.p.Term.Lock()
	line := g.p.Term.Row(g.v.vtRow(y))
	for i := 0; i < len(line) && i < g.cols; i++ {
		if c := line[i].Char; c != 0 {
			out[i] = c
		}
	}
	g.p.Term.Unlock()
	g.cache[y] = out
	return out
}

// extract returns the selected text with trailing blanks trimmed per line.
func (m *Mux) extract(s *Selection) string {
	g := newGrid(s.P)
	sx, sy, ex, ey := s.ordered()
	sy, ey = max(0, sy), min(g.total-1, ey)
	var lines []string
	for y := sy; y <= ey; y++ {
		a, b := 0, g.cols-1
		switch s.Kind {
		case 'b':
			a, b = min(s.AX, s.CX), max(s.AX, s.CX)
		case 'c':
			if y == sy {
				a = sx
			}
			if y == ey {
				b = ex
			}
		}
		a, b = max(0, a), min(g.cols-1, b)
		line := ""
		if a <= b {
			line = strings.TrimRight(string(g.row(y)[a:b+1]), " ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// copyText puts text on the clipboard (OSC 52 + native tool) and remembers it
// for the paste action.
func (m *Mux) copyText(text string) {
	if text == "" {
		return
	}
	m.clip = text
	m.rend.Raw(clipboard.OSC52(text))
	go clipboard.CopyNative(text)
	m.notify(fmt.Sprintf("copied %d chars", utf8.RuneCountInString(text)))
}

func (m *Mux) paste() {
	p := m.focusedPane()
	if p == nil || p.Dead {
		return
	}
	text := m.clip
	if text == "" {
		text = clipboard.Paste()
	}
	if text == "" {
		m.notify("nothing to paste")
		return
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r")
	_, _ = p.Write([]byte(text))
}
