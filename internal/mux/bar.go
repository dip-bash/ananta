package mux

import (
	"fmt"
	"os"
	"time"

	"ananta/internal/config"
	"ananta/internal/screen"
	"ananta/internal/stats"
	"ananta/internal/width"
)

// seg is one styled run of text in the bar. tab >= 0 marks a clickable
// workspace tab.
type seg struct {
	text   string
	fg, bg screen.Col
	attr   uint8
	tab    int
}

var warnColor = screen.RGB(0xbf, 0x61, 0x6a)

func (m *Mux) plain(s string) seg {
	return seg{text: s, fg: m.th.statusFG, bg: m.th.statusBG, tab: -1}
}

func human(b float64) string {
	const units = "BKMGT"
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%.0fB", b)
	case b < 10:
		return fmt.Sprintf("%.1f%c", b, units[i])
	}
	return fmt.Sprintf("%.0f%c", b, units[i])
}

// widget renders one named bar widget; an empty result hides it.
func (m *Mux) widget(name string, snap stats.Snapshot, now time.Time) []seg {
	bar := m.cfg.Bar
	switch name {
	case "workspaces":
		var out []seg
		for i, ws := range m.ws {
			if i != m.cur && len(ws.Cols) == 0 {
				continue
			}
			s := seg{text: fmt.Sprintf(" %d ", i+1), fg: m.th.statusFG, bg: m.th.statusBG, tab: i}
			if i == m.cur {
				s.fg, s.bg, s.attr = m.th.statusBG, m.th.accent, screen.AttrBold
			}
			out = append(out, s)
		}
		return out
	case "columns":
		ws := m.curWS()
		if len(ws.Cols) == 0 {
			return nil
		}
		var out []seg
		for i := range ws.Cols {
			if i == ws.Focus {
				out = append(out, seg{text: "█", fg: m.th.accent, bg: m.th.statusBG, tab: -1})
			} else {
				out = append(out, seg{text: "░", fg: m.th.statusFG, bg: m.th.statusBG, tab: -1})
			}
		}
		return append(out, m.plain(fmt.Sprintf(" %d/%d", ws.Focus+1, len(ws.Cols))))
	case "mode":
		var out []seg
		tag := func(s string) {
			out = append(out, seg{text: " " + s + " ", fg: m.th.statusBG, bg: m.th.accent, attr: screen.AttrBold, tab: -1})
		}
		if p := m.focusedPane(); p != nil && m.cm == nil {
			if n := linesAbove(p); n > 0 {
				tag(fmt.Sprintf("↑%d", n))
				out = append(out, m.plain(" type or shift+pgdn to return"))
			}
		}
		switch {
		case m.cm != nil:
			tag(m.cm.label())
			out = append(out, m.plain(" y copy · esc exit"))
		case m.prefixPending:
			tag("PREFIX")
		case m.overview:
			tag("OVERVIEW")
		}
		if m.scrollMode == "center" {
			out = append(out, seg{text: "centered", fg: m.th.accent, bg: m.th.statusBG, tab: -1})
		}
		return out
	case "message":
		if m.msg != "" && now.Before(m.msgUntil) {
			return []seg{{text: truncate(m.msg, max(10, m.W*2/3)), fg: m.th.accent, bg: m.th.statusBG, tab: -1}}
		}
	case "title":
		if p := m.focusedPane(); p != nil {
			p.Term.Lock()
			t := p.Term.Title()
			p.Term.Unlock()
			if t == "" {
				t = p.Name
			}
			return []seg{m.plain(truncate(t, 30))}
		}
	case "help":
		if k := m.cfg.KeyFor("help"); k != "" {
			return []seg{m.plain(k + " help")}
		}
	case "cpu":
		if snap.CPU >= 0 {
			s := m.plain(fmt.Sprintf("cpu %.0f%%", snap.CPU))
			if snap.CPU >= 90 {
				s.fg = warnColor
			}
			return []seg{s}
		}
	case "mem":
		if snap.MemTotal > 0 {
			pct := 100 * float64(snap.MemUsed) / float64(snap.MemTotal)
			switch bar.MemStyle {
			case "percent":
				return []seg{m.plain(fmt.Sprintf("mem %.0f%%", pct))}
			case "both":
				return []seg{m.plain(fmt.Sprintf("mem %s/%s %.0f%%", human(float64(snap.MemUsed)), human(float64(snap.MemTotal)), pct))}
			}
			return []seg{m.plain(fmt.Sprintf("mem %s/%s", human(float64(snap.MemUsed)), human(float64(snap.MemTotal))))}
		}
	case "net":
		if snap.NetDown >= 0 {
			return []seg{m.plain(fmt.Sprintf("↓%s ↑%s", human(snap.NetDown), human(snap.NetUp)))}
		}
	case "battery":
		if snap.Battery >= 0 {
			text := fmt.Sprintf("bat %d%%", snap.Battery)
			if snap.Charging {
				text += "+"
			}
			s := m.plain(text)
			if snap.Battery <= 20 && !snap.Charging {
				s.fg = warnColor
			}
			return []seg{s}
		}
	case "load":
		if snap.LoadOK {
			return []seg{m.plain(fmt.Sprintf("load %.2f", snap.Load1))}
		}
	case "time":
		return []seg{m.plain(now.Format(bar.TimeFormat))}
	case "date":
		return []seg{m.plain(now.Format(bar.DateFormat))}
	case "host":
		if h, err := os.Hostname(); err == nil {
			return []seg{m.plain(h)}
		}
	case "user":
		if u := os.Getenv("USER"); u != "" {
			return []seg{m.plain(u)}
		}
	default:
		if w, ok := bar.Widgets[name]; ok {
			if out := snap.Custom[name]; out != "" {
				s := m.plain(w.Prefix + out)
				if w.Color != "" {
					s.fg = config.ParseColor(w.Color)
				}
				return []seg{s}
			}
		}
	}
	return nil
}

func segsWidth(segs []seg) (n int) {
	for _, s := range segs {
		n += width.String(s.text)
	}
	return
}

// joinGroups concatenates widget outputs with the separator between them.
func (m *Mux) joinGroups(groups [][]seg) []seg {
	var out []seg
	for i, g := range groups {
		if i > 0 {
			out = append(out, seg{text: m.cfg.Bar.Separator, fg: m.th.border, bg: m.th.statusBG, tab: -1})
		}
		out = append(out, g...)
	}
	return out
}

func (m *Mux) drawSegs(scr *screen.Screen, y, x int, segs []seg) int {
	for _, s := range segs {
		x0 := x
		x = scr.Text(x, y, s.text, s.fg, s.bg, s.attr)
		if s.tab >= 0 {
			m.tabs = append(m.tabs, tabHit{x0: x0, x1: x, ws: s.tab})
		}
	}
	return x
}

func (m *Mux) drawBar(scr *screen.Screen) {
	m.tabs = m.tabs[:0]
	if !m.barVisible() {
		return
	}
	y := m.barY()
	scr.Fill(0, y, m.W, 1, screen.Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	snap := m.stats.Snapshot()
	now := time.Now()
	build := func(names []string) [][]seg {
		var groups [][]seg
		for _, n := range names {
			if s := m.widget(n, snap, now); len(s) > 0 {
				groups = append(groups, s)
			}
		}
		return groups
	}
	left, center, right := build(m.cfg.Bar.Left), build(m.cfg.Bar.Center), build(m.cfg.Bar.Right)

	// on narrow screens drop the right-hand widgets from the outside in
	// (the clock and help hint, listed last, survive longest)
	L := m.joinGroups(left)
	lw := segsWidth(L)
	C := m.joinGroups(center)
	cw := segsWidth(C)
	if cw > 0 { // a message (or a configured centre widget) outranks the right-hand widgets
		cw += 4
	}
	for len(right) > 0 && segsWidth(m.joinGroups(right))+lw+cw+4 > m.W {
		right = right[1:]
	}
	R := m.joinGroups(right)
	rw := segsWidth(R)

	x := m.drawSegs(scr, y, 1, L)
	rx := m.W - rw - 1
	if rw > 0 && rx > x {
		m.drawSegs(scr, y, rx, R)
	} else {
		rx = m.W
	}
	if cw := segsWidth(C); cw > 0 {
		cx := (m.W - cw) / 2
		if cx > x+1 && cx+cw < rx-1 {
			m.drawSegs(scr, y, cx, C)
		} else if cw <= rx-x-2 && x+1 < rx { // doesn't fit centered: use the free gap
			m.drawSegs(scr, y, x+1, C)
		}
	}
}
