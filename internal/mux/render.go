package mux

import (
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/hinshun/vt10x"

	"ananta/internal/pane"
	"ananta/internal/screen"
)

func mapColor(c vt10x.Color) screen.Col {
	switch {
	case c >= 1<<24:
		return screen.ColDefault
	case c < 256:
		return screen.Col(c)
	}
	return screen.Col(screen.TrueBit | int32(c&0xffffff))
}

func glyphCell(g vt10x.Glyph) screen.Cell {
	c := screen.Cell{R: g.Char, FG: mapColor(g.FG), BG: mapColor(g.BG)}
	if c.R == 0 {
		c.R = ' '
	}
	if g.Mode&1 != 0 {
		c.A |= screen.AttrReverse
	}
	if g.Mode&2 != 0 {
		c.A |= screen.AttrUnderline
	}
	if g.Mode&4 != 0 {
		c.A |= screen.AttrBold
	}
	if g.Mode&16 != 0 {
		c.A |= screen.AttrItalic
	}
	return c
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func (m *Mux) drawBox(scr *screen.Screen, x, y, w, h int, bc, bg screen.Col) {
	bs := m.bs
	cell := func(r rune) screen.Cell { return screen.Cell{R: r, FG: bc, BG: bg} }
	scr.Set(x, y, cell(bs.TL))
	scr.Set(x+w-1, y, cell(bs.TR))
	scr.Set(x, y+h-1, cell(bs.BL))
	scr.Set(x+w-1, y+h-1, cell(bs.BR))
	for i := 1; i < w-1; i++ {
		scr.Set(x+i, y, cell(bs.H))
		scr.Set(x+i, y+h-1, cell(bs.H))
	}
	for j := 1; j < h-1; j++ {
		scr.Set(x, y+j, cell(bs.V))
		scr.Set(x+w-1, y+j, cell(bs.V))
	}
}

// overlay is what gets painted over a pane's content: a selection highlight
// and (in copy mode) the copy cursor.
type overlay struct {
	sel    *Selection
	cx, cy int
	hasCur bool
}

func (m *Mux) overlayFor(p *pane.Pane) overlay {
	var ov overlay
	if m.sel != nil && m.sel.P == p {
		ov.sel = m.sel
	}
	if m.cm != nil && m.cm.P == p {
		ov.cx, ov.cy, ov.hasCur = m.cm.X, m.cm.Y, true
		if s := m.cm.selection(); s != nil {
			ov.sel = s
		}
	}
	return ov
}

func (m *Mux) drawPane(scr *screen.Screen, p *pane.Pane, x, y, w, h int, focused, thumb bool) (cx, cy int, cursor bool) {
	if w < 2 || h < 2 {
		return
	}
	bc, tc, ta := m.th.border, m.th.title, uint8(0)
	if focused {
		bc, tc, ta = m.th.focusBorder, m.th.focusTitle, screen.AttrBold
	}
	m.drawBox(scr, x, y, w, h, bc, screen.ColDefault)

	iw, ih := w-2, h-2
	ov := m.overlayFor(p)
	p.Term.Lock()
	title := p.Term.Title()
	tcols, trows := p.Term.Size()
	if !thumb {
		for row := 0; row < ih; row++ {
			for col := 0; col < iw; col++ {
				if col >= tcols || row >= trows {
					scr.Set(x+1+col, y+1+row, screen.Blank)
					continue
				}
				c := glyphCell(p.Term.Cell(col, row))
				if ov.sel != nil && ov.sel.Contains(col, row) {
					c.FG, c.BG, c.A = m.th.selFG, m.th.selBG, c.A&^screen.AttrReverse
				}
				if ov.hasCur && col == ov.cx && row == ov.cy {
					c.FG, c.BG, c.A = m.th.statusBG, m.th.accent, c.A&^screen.AttrReverse
				}
				scr.Set(x+1+col, y+1+row, c)
			}
		}
	} else if iw > 0 && ih > 0 && tcols > 0 && trows > 0 {
		// scaled-down snapshot: each thumbnail cell shows the first visible
		// glyph of the block of real cells it covers
		for row := 0; row < ih; row++ {
			sy0 := row * trows / ih
			sy1 := max(sy0+1, (row+1)*trows/ih)
			for col := 0; col < iw; col++ {
				sx0 := col * tcols / iw
				sx1 := max(sx0+1, (col+1)*tcols/iw)
				best := glyphCell(p.Term.Cell(sx0, sy0))
				if best.R <= ' ' {
				search:
					for sy := sy0; sy < sy1 && sy < trows; sy++ {
						for sx := sx0; sx < sx1 && sx < tcols; sx++ {
							g := p.Term.Cell(sx, sy)
							if g.Char > ' ' {
								c := glyphCell(g)
								best.R, best.FG, best.A = c.R, c.FG, c.A
								break search
							}
						}
					}
				}
				scr.Set(x+1+col, y+1+row, best)
			}
		}
	}
	cur := p.Term.Cursor()
	visible := p.Term.CursorVisible()
	p.Term.Unlock()

	if title == "" {
		title = p.Name
	}
	if w > 6 {
		if t := truncate(title, w-6); t != "" {
			scr.Text(x+2, y, " "+t+" ", tc, screen.ColDefault, ta)
		}
	}
	if !thumb && focused && visible && m.cm == nil && cur.X < iw && cur.Y < ih && cur.X < tcols && cur.Y < trows {
		return x + 1 + cur.X, y + 1 + cur.Y, true
	}
	return
}

func (m *Mux) render() {
	m.syncSizes()
	scr := m.rend.Cur()
	scr.Clear()
	m.hits = m.hits[:0]

	s := m.scale
	thumb := s < 0.995
	tt, tileH := m.tileTop(), m.tileH()
	rowHf, pitch := m.rowGeom()
	rowH := tileH
	if thumb {
		rowH = max(4, int(math.Round(rowHf)))
	}
	showLabels := m.gap() > 0.9
	cx, cy, showCur := 0, 0, false

	for k, wi := range m.visibleWS() {
		ws := m.ws[wi]
		top := tt + int(math.Round(float64(k)*pitch-m.ovY))
		if top >= tt+tileH || top+rowH <= tt {
			continue
		}
		cum := 0.0
		for ci, col := range ws.Cols {
			x0 := int(math.Round((cum - ws.ViewX) * s))
			cum += col.CurW
			x1 := int(math.Round((cum - ws.ViewX) * s))
			w := x1 - x0
			if w <= 0 || x1 <= 0 || x0 >= m.W {
				continue
			}
			y := top
			for pi, p := range col.Panes {
				h := paneHeight(rowH, len(col.Panes), pi)
				focused := wi == m.cur && ci == ws.Focus && pi == col.Focus
				if x, yy, ok := m.drawPane(scr, p, x0, y, w, h, focused, thumb); ok &&
					x >= 0 && x < m.W && yy >= tt && yy < tt+tileH {
					cx, cy, showCur = x, yy, true
				}
				m.hits = append(m.hits, hit{p: p, ws: wi, ci: ci, pi: pi, x: x0, y: y, w: w, h: h, thumb: thumb})
				y += h
			}
		}
		if len(ws.Cols) == 0 {
			msg := "empty workspace — open a terminal"
			if key := m.cfg.KeyFor("new_pane"); key != "" {
				msg = "empty workspace — press " + key + " to open a terminal"
			}
			scr.Text(max(0, (m.W-utf8.RuneCountInString(msg))/2), top+rowH/2, truncate(msg, m.W), m.th.title, screen.ColDefault, 0)
		}
		if showLabels && top-1 >= tt {
			fg, bg := m.th.statusFG, m.th.statusBG
			if wi == m.cur {
				fg, bg = m.th.statusBG, m.th.accent
			}
			scr.Text(1, top-1, fmt.Sprintf(" %d ", wi+1), fg, bg, screen.AttrBold)
		}
	}
	m.drawBar(scr)
	if m.showHelp {
		m.drawHelp(scr)
		showCur = false
	}
	m.rend.Flush(cx, cy, showCur)
}
