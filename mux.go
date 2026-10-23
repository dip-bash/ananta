package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hinshun/vt10x"
	"golang.org/x/term"
)

const (
	minColW       = 12
	numWorkspaces = 9
)

type Column struct {
	Panes     []*Pane
	Focus     int
	WFrac     float64 // target width, fraction of screen
	SavedFrac float64 // width to restore after un-maximize
	CurW      float64 // animated, displayed width in cells
}

type Workspace struct {
	Cols    []*Column
	Focus   int
	ViewX   float64 // animated viewport offset into the strip
	TargetX float64
}

type theme struct {
	border, focusBorder, title, focusTitle, statusBG, statusFG, accent Col
}

type Mux struct {
	cfg    *Config
	th     theme
	bs     borderSet
	ws     []*Workspace
	cur    int
	W, H   int
	rend   *Renderer
	nextID int

	dirty chan struct{}
	exits chan *Pane

	prefixPending bool
	showHelp      bool
	quit          bool
	msg           string
	msgUntil      time.Time
}

func removeAt[T any](s []T, i int) []T { return append(s[:i], s[i+1:]...) }

func newMux(cfg *Config, out *bufio.Writer) *Mux {
	m := &Mux{
		cfg:   cfg,
		bs:    borderFor(cfg.BorderStyle),
		rend:  NewRenderer(out),
		dirty: make(chan struct{}, 1),
		exits: make(chan *Pane, 64),
		th: theme{
			border: parseColor(cfg.Theme.Border), focusBorder: parseColor(cfg.Theme.FocusBorder),
			title: parseColor(cfg.Theme.Title), focusTitle: parseColor(cfg.Theme.FocusTitle),
			statusBG: parseColor(cfg.Theme.StatusBG), statusFG: parseColor(cfg.Theme.StatusFG),
			accent: parseColor(cfg.Theme.Accent),
		},
	}
	for i := 0; i < numWorkspaces; i++ {
		m.ws = append(m.ws, &Workspace{})
	}
	if len(cfg.warnings) > 0 {
		m.notify(strings.Join(cfg.warnings, "; "))
	}
	return m
}

func (m *Mux) notify(s string) { m.msg, m.msgUntil = s, time.Now().Add(4*time.Second) }

func (m *Mux) curWS() *Workspace { return m.ws[m.cur] }

func (m *Mux) focusedCol() *Column {
	ws := m.curWS()
	if len(ws.Cols) == 0 {
		return nil
	}
	return ws.Cols[ws.Focus]
}

func (m *Mux) focusedPane() *Pane {
	c := m.focusedCol()
	if c == nil {
		return nil
	}
	return c.Panes[c.Focus]
}

func (m *Mux) totalPanes() int {
	n := 0
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			n += len(c.Panes)
		}
	}
	return n
}

func (m *Mux) onResize() {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 1 || h < 2 {
		return
	}
	m.W, m.H = w, h
	m.rend.Resize(w, h)
}

func (m *Mux) targetW(c *Column) int {
	if m.W < minColW {
		return m.W
	}
	w := int(math.Round(c.WFrac * float64(m.W)))
	return max(minColW, min(w, m.W))
}

func paneHeight(total, n, i int) int {
	h := total / n
	if i < total%n {
		h++
	}
	return h
}

// ---------------------------------------------------------------- layout ops

func (ws *Workspace) detachAt(ci, pi int) *Pane {
	col := ws.Cols[ci]
	p := col.Panes[pi]
	col.Panes = removeAt(col.Panes, pi)
	if len(col.Panes) == 0 {
		ws.Cols = removeAt(ws.Cols, ci)
		if ci < ws.Focus {
			ws.Focus--
		}
		ws.Focus = max(0, min(ws.Focus, len(ws.Cols)-1))
	} else {
		if pi < col.Focus {
			col.Focus--
		}
		col.Focus = max(0, min(col.Focus, len(col.Panes)-1))
	}
	return p
}

func (m *Mux) newColumnFor(ws *Workspace, p *Pane) {
	col := &Column{Panes: []*Pane{p}, WFrac: m.cfg.DefaultColumnWidth}
	if !m.cfg.Animate {
		col.CurW = float64(m.targetW(col))
	}
	idx := 0
	if len(ws.Cols) > 0 {
		idx = ws.Focus + 1
	}
	ws.Cols = append(ws.Cols, nil)
	copy(ws.Cols[idx+1:], ws.Cols[idx:])
	ws.Cols[idx] = col
	ws.Focus = idx
}

func (m *Mux) spawn(cmdline string) (*Pane, error) {
	m.nextID++
	w := int(m.cfg.DefaultColumnWidth*float64(m.W)) - 2
	return newPane(m.nextID, m.cfg.Shell, cmdline, w, m.H-3, m.dirty, m.exits)
}

func (m *Mux) newPane(cmdline string) {
	p, err := m.spawn(cmdline)
	if err != nil {
		m.notify("spawn failed: " + err.Error())
		return
	}
	m.newColumnFor(m.curWS(), p)
}

func (m *Mux) newPaneBelow(cmdline string) {
	col := m.focusedCol()
	if col == nil {
		m.newPane(cmdline)
		return
	}
	p, err := m.spawn(cmdline)
	if err != nil {
		m.notify("spawn failed: " + err.Error())
		return
	}
	i := col.Focus + 1
	col.Panes = append(col.Panes, nil)
	copy(col.Panes[i+1:], col.Panes[i:])
	col.Panes[i] = p
	col.Focus = i
}

func (m *Mux) findPane(p *Pane) (ws *Workspace, ci, pi int, ok bool) {
	for _, w := range m.ws {
		for c, col := range w.Cols {
			for i, q := range col.Panes {
				if q == p {
					return w, c, i, true
				}
			}
		}
	}
	return nil, 0, 0, false
}

func (m *Mux) removePane(p *Pane) {
	p.Close()
	if ws, ci, pi, ok := m.findPane(p); ok {
		ws.detachAt(ci, pi)
	}
	if m.totalPanes() == 0 && m.cfg.QuitOnLastExit {
		m.quit = true
	}
}

func (m *Mux) shutdown() {
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				p.Close()
			}
		}
	}
}

// ---------------------------------------------------------------- actions

func (m *Mux) run(a Action) {
	ws := m.curWS()
	col := m.focusedCol()
	switch a.Name {
	case "focus_left":
		ws.Focus = max(0, ws.Focus-1)
	case "focus_right":
		ws.Focus = max(0, min(len(ws.Cols)-1, ws.Focus+1))
	case "focus_up":
		if col != nil {
			col.Focus = max(0, col.Focus-1)
		}
	case "focus_down":
		if col != nil {
			col.Focus = min(len(col.Panes)-1, col.Focus+1)
		}
	case "first_column":
		ws.Focus = 0
	case "last_column":
		ws.Focus = max(0, len(ws.Cols)-1)
	case "move_left":
		if col != nil && ws.Focus > 0 {
			ws.Cols[ws.Focus], ws.Cols[ws.Focus-1] = ws.Cols[ws.Focus-1], ws.Cols[ws.Focus]
			ws.Focus--
		}
	case "move_right":
		if col != nil && ws.Focus < len(ws.Cols)-1 {
			ws.Cols[ws.Focus], ws.Cols[ws.Focus+1] = ws.Cols[ws.Focus+1], ws.Cols[ws.Focus]
			ws.Focus++
		}
	case "move_up":
		if col != nil && col.Focus > 0 {
			col.Panes[col.Focus], col.Panes[col.Focus-1] = col.Panes[col.Focus-1], col.Panes[col.Focus]
			col.Focus--
		}
	case "move_down":
		if col != nil && col.Focus < len(col.Panes)-1 {
			col.Panes[col.Focus], col.Panes[col.Focus+1] = col.Panes[col.Focus+1], col.Panes[col.Focus]
			col.Focus++
		}
	case "new_pane":
		m.newPane(a.Arg)
	case "new_pane_below":
		m.newPaneBelow(a.Arg)
	case "close_pane":
		if p := m.focusedPane(); p != nil {
			m.removePane(p)
		}
	case "stack_left":
		if col != nil && ws.Focus > 0 {
			ci := ws.Focus
			p := ws.detachAt(ci, col.Focus)
			left := ws.Cols[ci-1]
			left.Panes = append(left.Panes, p)
			left.Focus = len(left.Panes) - 1
			ws.Focus = ci - 1
		}
	case "unstack":
		if col != nil && len(col.Panes) > 1 {
			ci := ws.Focus
			p := ws.detachAt(ci, col.Focus)
			ws.Focus = ci
			m.newColumnFor(ws, p)
		}
	case "grow":
		if col != nil {
			col.WFrac = math.Min(1, col.WFrac+m.cfg.ResizeStep)
		}
	case "shrink":
		if col != nil {
			col.WFrac = math.Max(float64(minColW)/float64(max(m.W, 1)), col.WFrac-m.cfg.ResizeStep)
		}
	case "cycle_width":
		if col != nil {
			next := m.cfg.ColumnWidths[0]
			for _, w := range m.cfg.ColumnWidths {
				if w > col.WFrac+0.01 {
					next = w
					break
				}
			}
			col.WFrac = next
		}
	case "maximize":
		if col != nil {
			if col.WFrac < 0.999 {
				col.SavedFrac, col.WFrac = col.WFrac, 1
			} else if col.SavedFrac > 0 {
				col.WFrac = col.SavedFrac
			} else {
				col.WFrac = m.cfg.DefaultColumnWidth
			}
		}
	case "workspace_next":
		m.cur = (m.cur + 1) % len(m.ws)
	case "workspace_prev":
		m.cur = (m.cur + len(m.ws) - 1) % len(m.ws)
	case "goto_workspace":
		if n, err := strconv.Atoi(a.Arg); err == nil && n >= 1 && n <= len(m.ws) {
			m.cur = n - 1
		}
	case "move_to_workspace":
		n, err := strconv.Atoi(a.Arg)
		if err != nil || n < 1 || n > len(m.ws) || n-1 == m.cur || col == nil {
			return
		}
		p := ws.detachAt(ws.Focus, col.Focus)
		m.newColumnFor(m.ws[n-1], p)
	case "help":
		m.showHelp = !m.showHelp
	case "quit":
		m.quit = true
	}
}

// ---------------------------------------------------------------- input

func (m *Mux) translate(k Key) []byte {
	r := k.Raw
	if len(r) == 3 && r[0] == 0x1b && r[1] == '[' && strings.IndexByte("ABCDHF", r[2]) >= 0 {
		if p := m.focusedPane(); p != nil {
			p.Term.Lock()
			app := p.Term.Mode()&vt10x.ModeAppCursor != 0
			p.Term.Unlock()
			if app {
				return []byte{0x1b, 'O', r[2]}
			}
		}
	}
	return r
}

func (m *Mux) handleKeys(ks []Key) {
	var pending []byte
	flush := func() {
		if len(pending) > 0 {
			if p := m.focusedPane(); p != nil && !p.Dead {
				_, _ = p.PTY.Write(pending)
			}
			pending = pending[:0]
		}
	}
	for _, k := range ks {
		if m.showHelp {
			m.showHelp = false
			continue
		}
		if m.prefixPending {
			m.prefixPending = false
			if k.Name == m.cfg.prefixKey {
				pending = append(pending, k.Raw...)
			} else if a, ok := m.cfg.prefixmap[k.Name]; ok {
				flush()
				m.run(a)
			}
			continue
		}
		if m.cfg.prefixKey != "" && k.Name == m.cfg.prefixKey {
			flush()
			m.prefixPending = true
			continue
		}
		if a, ok := m.cfg.keymap[k.Name]; ok && k.Name != "" {
			flush()
			m.run(a)
			continue
		}
		pending = append(pending, m.translate(k)...)
	}
	flush()
}

// ---------------------------------------------------------------- animation

// syncSizes resizes every PTY to the *target* layout so apps reflow once,
// while the on-screen rectangle animates towards it.
func (m *Mux) syncSizes() {
	tileH := m.H - 1
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			tw := m.targetW(c)
			for i, p := range c.Panes {
				p.Resize(tw-2, paneHeight(tileH, len(c.Panes), i)-2)
			}
		}
	}
}

func (m *Mux) updateTarget(ws *Workspace) {
	if len(ws.Cols) == 0 {
		ws.TargetX = 0
		return
	}
	total, fx, fw := 0.0, 0.0, 0.0
	for i, c := range ws.Cols {
		w := float64(m.targetW(c))
		if i == ws.Focus {
			fx, fw = total, w
		}
		total += w
	}
	W := float64(m.W)
	if m.cfg.ScrollMode == "center" {
		ws.TargetX = fx + fw/2 - W/2
	} else {
		if fw >= W || fx < ws.TargetX {
			ws.TargetX = fx
		} else if fx+fw > ws.TargetX+W {
			ws.TargetX = fx + fw - W
		}
	}
	ws.TargetX = math.Max(0, math.Min(ws.TargetX, math.Max(0, total-W)))
}

func (m *Mux) step(dt float64) bool {
	ws := m.curWS()
	m.updateTarget(ws)
	alpha := 1.0
	if m.cfg.Animate {
		alpha = 1 - math.Exp(-m.cfg.AnimationSpeed*dt)
	}
	animating := false
	ease := func(cur *float64, target float64) {
		d := target - *cur
		if math.Abs(d) < 0.05 || alpha >= 1 {
			*cur = target
			return
		}
		mv := d * alpha
		if math.Abs(mv) < 0.6 { // never crawl: keep at least ~0.6 cell/frame
			mv = math.Copysign(math.Min(0.6, math.Abs(d)), d)
		}
		*cur += mv
		animating = true
	}
	for _, c := range ws.Cols {
		ease(&c.CurW, float64(m.targetW(c)))
	}
	ease(&ws.ViewX, ws.TargetX)
	return animating
}

// ---------------------------------------------------------------- render

func mapColor(c vt10x.Color) Col {
	switch {
	case c >= 1<<24:
		return ColDefault
	case c < 256:
		return Col(c)
	}
	return Col(trueBit | int32(c&0xffffff))
}

func glyphCell(g vt10x.Glyph) Cell {
	c := Cell{R: g.Char, FG: mapColor(g.FG), BG: mapColor(g.BG)}
	if c.R == 0 {
		c.R = ' '
	}
	if g.Mode&1 != 0 {
		c.A |= AttrReverse
	}
	if g.Mode&2 != 0 {
		c.A |= AttrUnderline
	}
	if g.Mode&4 != 0 {
		c.A |= AttrBold
	}
	if g.Mode&16 != 0 {
		c.A |= AttrItalic
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

func (m *Mux) drawPane(scr *Screen, p *Pane, x, y, w, h int, focused bool) (cx, cy int, cursor bool) {
	if w < 2 || h < 2 {
		return
	}
	bc, tc, ta := m.th.border, m.th.title, uint8(0)
	if focused {
		bc, tc, ta = m.th.focusBorder, m.th.focusTitle, AttrBold
	}
	bcell := func(r rune) Cell { return Cell{R: r, FG: bc, BG: ColDefault} }
	bs := m.bs
	scr.Set(x, y, bcell(bs.tl))
	scr.Set(x+w-1, y, bcell(bs.tr))
	scr.Set(x, y+h-1, bcell(bs.bl))
	scr.Set(x+w-1, y+h-1, bcell(bs.br))
	for i := 1; i < w-1; i++ {
		scr.Set(x+i, y, bcell(bs.h))
		scr.Set(x+i, y+h-1, bcell(bs.h))
	}
	for j := 1; j < h-1; j++ {
		scr.Set(x, y+j, bcell(bs.v))
		scr.Set(x+w-1, y+j, bcell(bs.v))
	}

	iw, ih := w-2, h-2
	p.Term.Lock()
	title := p.Term.Title()
	tcols, trows := p.Term.Size()
	for row := 0; row < ih; row++ {
		for col := 0; col < iw; col++ {
			if col < tcols && row < trows {
				scr.Set(x+1+col, y+1+row, glyphCell(p.Term.Cell(col, row)))
			} else {
				scr.Set(x+1+col, y+1+row, blank)
			}
		}
	}
	cur := p.Term.Cursor()
	visible := p.Term.CursorVisible()
	p.Term.Unlock()

	if title == "" {
		title = p.Name
	}
	if t := truncate(title, w-6); t != "" && w > 6 {
		scr.Text(x+2, y, " "+t+" ", tc, ColDefault, ta)
	}
	if focused && visible && cur.X < iw && cur.Y < ih && cur.X < tcols && cur.Y < trows {
		return x + 1 + cur.X, y + 1 + cur.Y, true
	}
	return
}

func (m *Mux) render() {
	m.syncSizes()
	scr := m.rend.cur
	scr.Clear()
	ws := m.curWS()
	tileH := m.H - 1
	cx, cy, showCur := 0, 0, false

	fx := -ws.ViewX
	for ci, col := range ws.Cols {
		x0 := int(math.Round(fx))
		fx += col.CurW
		x1 := int(math.Round(fx))
		w := x1 - x0
		if w <= 0 || x1 <= 0 || x0 >= m.W {
			continue
		}
		y := 0
		for pi, p := range col.Panes {
			h := paneHeight(tileH, len(col.Panes), pi)
			focused := ci == ws.Focus && pi == col.Focus
			if x, yy, ok := m.drawPane(scr, p, x0, y, w, h, focused); ok {
				cx, cy, showCur = x, yy, true
			}
			y += h
		}
	}
	if len(ws.Cols) == 0 {
		msg := "no panes — press alt+enter to open a terminal"
		scr.Text(max(0, (m.W-utf8.RuneCountInString(msg))/2), tileH/2, msg, m.th.title, ColDefault, 0)
	}
	m.drawStatus(scr)
	if m.showHelp {
		m.drawHelp(scr)
		showCur = false
	}
	m.rend.Flush(cx, cy, showCur)
}

func (m *Mux) drawStatus(scr *Screen) {
	y := m.H - 1
	scr.Fill(0, y, m.W, 1, Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	x := 1
	for i, ws := range m.ws {
		if i != m.cur && len(ws.Cols) == 0 {
			continue
		}
		label := fmt.Sprintf(" %d ", i+1)
		if i == m.cur {
			x = scr.Text(x, y, label, m.th.statusBG, m.th.accent, AttrBold)
		} else {
			x = scr.Text(x, y, label, m.th.statusFG, m.th.statusBG, 0)
		}
	}
	ws := m.curWS()
	x += 2
	for i := range ws.Cols {
		if i == ws.Focus {
			x = scr.Text(x, y, "█", m.th.accent, m.th.statusBG, 0)
		} else {
			x = scr.Text(x, y, "░", m.th.statusFG, m.th.statusBG, 0)
		}
	}
	if len(ws.Cols) > 0 {
		x = scr.Text(x+1, y, fmt.Sprintf("%d/%d", ws.Focus+1, len(ws.Cols)), m.th.statusFG, m.th.statusBG, 0)
	}
	right := "alt+/ help "
	rfg, rbg, ra := m.th.statusFG, m.th.statusBG, uint8(0)
	switch {
	case m.prefixPending:
		right, rfg, rbg, ra = " PREFIX ", m.th.statusBG, m.th.accent, AttrBold
	case m.msg != "" && time.Now().Before(m.msgUntil):
		right = m.msg + " "
	}
	rx := m.W - utf8.RuneCountInString(right)
	if rx > x+1 {
		scr.Text(rx, y, right, rfg, rbg, ra)
	}
}

func (m *Mux) drawHelp(scr *Screen) {
	lines := m.cfg.helpLines()
	maxLen := 0
	for _, l := range lines {
		maxLen = max(maxLen, utf8.RuneCountInString(l))
	}
	title := " shortcuts — any key to close "
	maxLen = max(maxLen, utf8.RuneCountInString(title))
	w := min(maxLen+4, m.W)
	avail := m.H - 1 - 2
	h := min(len(lines)+2, max(avail, 3))
	x, y := (m.W-w)/2, (m.H-1-h)/2
	scr.Fill(x, y, w, h, Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	bs := m.bs
	bc := func(r rune) Cell { return Cell{R: r, FG: m.th.focusBorder, BG: m.th.statusBG} }
	scr.Set(x, y, bc(bs.tl))
	scr.Set(x+w-1, y, bc(bs.tr))
	scr.Set(x, y+h-1, bc(bs.bl))
	scr.Set(x+w-1, y+h-1, bc(bs.br))
	for i := 1; i < w-1; i++ {
		scr.Set(x+i, y, bc(bs.h))
		scr.Set(x+i, y+h-1, bc(bs.h))
	}
	for j := 1; j < h-1; j++ {
		scr.Set(x, y+j, bc(bs.v))
		scr.Set(x+w-1, y+j, bc(bs.v))
	}
	scr.Text(x+2, y, truncate(title, w-4), m.th.focusTitle, m.th.statusBG, AttrBold)
	for i := 0; i < h-2 && i < len(lines); i++ {
		l := lines[i]
		if i == h-3 && len(lines) > h-2 {
			l = "…"
		}
		scr.Text(x+2, y+1+i, truncate(l, w-4), m.th.statusFG, m.th.statusBG, 0)
	}
}

// ---------------------------------------------------------------- loop

func (m *Mux) loop(keys <-chan []Key, winch, stop <-chan os.Signal) {
	tick := time.NewTicker(time.Second / time.Duration(m.cfg.FPS))
	defer tick.Stop()
	last := time.Now()
	need := true
	for !m.quit {
		select {
		case ks, ok := <-keys:
			if !ok {
				return
			}
			m.handleKeys(ks)
			need = true
		case <-m.dirty:
			need = true
		case p := <-m.exits:
			m.removePane(p)
			need = true
		case <-winch:
			m.onResize()
			need = true
		case <-stop:
			return
		case now := <-tick.C:
			dt := math.Min(now.Sub(last).Seconds(), 0.1)
			last = now
			animating := m.step(dt)
			if m.msg != "" && !time.Now().Before(m.msgUntil) {
				m.msg = ""
				need = true
			}
			if need || animating {
				m.render()
				need = animating
			}
		}
	}
}
