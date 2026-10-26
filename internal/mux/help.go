package mux

import (
	"fmt"

	"ananta/internal/screen"
	"ananta/internal/width"
)

// drawHelp paints the scrollable shortcut list, generated from the live config.
func (m *Mux) drawHelp(scr *screen.Screen) {
	lines := m.cfg.HelpLines()
	title := fmt.Sprintf(" shortcuts · leader = %s ", m.cfg.Leader)
	foot := " j/k pgup/pgdn scroll · esc close "
	maxLen := max(width.String(title), width.String(foot)+8)
	for _, l := range lines {
		maxLen = max(maxLen, width.String(l))
	}
	tt, tileH := m.tileTop(), m.tileH()
	w := min(maxLen+5, m.W)
	vis := max(1, min(len(lines), tileH-4))
	m.helpVis = vis
	maxScroll := max(0, len(lines)-vis)
	m.helpScroll = max(0, min(m.helpScroll, maxScroll))
	h := vis + 2
	x, y := (m.W-w)/2, tt+max(0, (tileH-h)/2)
	scr.Fill(x, y, w, h, screen.Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	m.drawBox(scr, x, y, w, h, m.th.focusBorder, m.th.statusBG)
	scr.Text(x+2, y, truncate(title, w-4), m.th.focusTitle, m.th.statusBG, screen.AttrBold)
	for i := 0; i < vis; i++ {
		scr.Text(x+2, y+1+i, truncate(lines[m.helpScroll+i], w-5), m.th.statusFG, m.th.statusBG, 0)
	}
	if len(lines) > vis {
		thumbH := max(1, vis*vis/len(lines))
		thumbY := 0
		if maxScroll > 0 {
			thumbY = m.helpScroll * (vis - thumbH) / maxScroll
		}
		for i := 0; i < vis; i++ {
			r, fg := '░', m.th.border
			if i >= thumbY && i < thumbY+thumbH {
				r, fg = '█', m.th.accent
			}
			scr.Set(x+w-2, y+1+i, screen.Cell{R: r, FG: fg, BG: m.th.statusBG})
		}
		counter := fmt.Sprintf(" %d-%d/%d ", m.helpScroll+1, m.helpScroll+vis, len(lines))
		scr.Text(x+w-2-width.String(counter), y+h-1, counter, m.th.statusFG, m.th.statusBG, 0)
	}
	scr.Text(x+2, y+h-1, truncate(foot, w-4), m.th.statusFG, m.th.statusBG, 0)
}
