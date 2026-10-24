package ananta

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
	minColW = 12
)

type Column struct {
	Panes     []*Pane
	Focus     int
	WFrac     float64 // target width, fraction of screen
	SavedFrac float64 // width to restore after un-maximize
	CurW      float64 // animated, displayed width in cells
}

type Workspace struct {
	Cols         []*Column
	Focus        int
	ViewX        float64 // animated viewport offset into the strip
	TargetX      float64
	ScrollMode   string
	Overview     bool
	OverviewCol  int
	OverviewPane int
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
	helpOffset    int
	quit          bool
	msg           string
	msgUntil      time.Time
	statusSampler *statusSampler
	panelHits     []panelHit
}

type paneRect struct {
	col, pane  int
	x, y, w, h int
}

type panelHit struct {
	x0, x1 int
	kind   string
	index  int
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
	m.ws = append(m.ws, &Workspace{ScrollMode: cfg.ScrollMode})
	m.statusSampler = newStatusSampler(func() {
		select {
		case m.dirty <- struct{}{}:
		default:
		}
	})
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
	return newPane(m.nextID, m.cfg.Shell, cmdline, w, m.contentH()-2, m.dirty, m.exits)
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

func (m *Mux) applyInitialLayout() {
	if len(m.cfg.InitialLayout) == 0 {
		m.newPane("")
		return
	}
	ws := m.curWS()
	for _, colLayout := range m.cfg.InitialLayout[0].Columns {
		width := colLayout.Width
		if width <= 0 || width > 1 {
			width = m.cfg.DefaultColumnWidth
		}
		p, err := m.spawn(colLayout.Command)
		if err != nil {
			m.notify("spawn failed: " + err.Error())
			continue
		}
		col := &Column{Panes: []*Pane{p}, WFrac: width}
		if !m.cfg.Animate {
			col.CurW = float64(m.targetW(col))
		}
		ws.Cols = append(ws.Cols, col)
	}
	if len(ws.Cols) > 0 {
		ws.Focus = 0
	}
}

func (m *Mux) ensureWorkspace(wsIdx int) {
	if wsIdx < 0 {
		return
	}
	for wsIdx >= len(m.ws) {
		m.ws = append(m.ws, &Workspace{ScrollMode: m.cfg.ScrollMode})
	}
	ws := m.ws[wsIdx]
	if len(ws.Cols) > 0 {
		return
	}
	if wsIdx < len(m.cfg.InitialLayout) {
		for _, colLayout := range m.cfg.InitialLayout[wsIdx].Columns {
			width := colLayout.Width
			if width <= 0 || width > 1 {
				width = m.cfg.DefaultColumnWidth
			}
			p, err := m.spawn(colLayout.Command)
			if err != nil {
				m.notify("spawn failed: " + err.Error())
				continue
			}
			col := &Column{Panes: []*Pane{p}, WFrac: width}
			if !m.cfg.Animate {
				col.CurW = float64(m.targetW(col))
			}
			ws.Cols = append(ws.Cols, col)
		}
		if len(ws.Cols) > 0 {
			ws.Focus = 0
		}
	}
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
	if m.statusSampler != nil {
		m.statusSampler.Close()
	}
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				p.Close()
			}
		}
	}
}

func (m *Mux) panelTop() bool {
	return m.cfg.Status.Position == "top"
}

func (m *Mux) panelY() int {
	if m.panelTop() {
		return 0
	}
	return m.H - 1
}

func (m *Mux) contentY() int {
	if m.panelTop() {
		return 1
	}
	return 0
}

func (m *Mux) contentH() int {
	return max(0, m.H-1)
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
		if m.cur+1 < len(m.ws) {
			m.cur++
		} else {
			m.cur = len(m.ws)
			m.ensureWorkspace(m.cur)
		}
	case "workspace_prev":
		if m.cur > 0 {
			m.cur--
		}
	case "goto_workspace":
		if n, err := strconv.Atoi(a.Arg); err == nil && n >= 1 && n <= len(m.ws)+1 {
			m.cur = n - 1
			m.ensureWorkspace(m.cur)
		}
	case "move_to_workspace":
		n, err := strconv.Atoi(a.Arg)
		if err != nil || n < 1 || n > len(m.ws) || n-1 == m.cur || col == nil {
			return
		}
		p := ws.detachAt(ws.Focus, col.Focus)
		m.newColumnFor(m.ws[n-1], p)
	case "overview":
		ws.Overview = !ws.Overview
		if ws.Overview {
			ws.OverviewCol = ws.Focus
			if col != nil {
				ws.OverviewPane = col.Focus
			}
		}
	case "help":
		m.showHelp = !m.showHelp
		if m.showHelp {
			m.helpOffset = 0
		}
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

func (m *Mux) focusPane(ci, pi int) {
	ws := m.curWS()
	if ci < 0 || ci >= len(ws.Cols) {
		return
	}
	col := ws.Cols[ci]
	if pi < 0 || pi >= len(col.Panes) {
		return
	}
	ws.Focus = ci
	col.Focus = pi
}

func (m *Mux) focusColumn(ci int) {
	ws := m.curWS()
	if ci < 0 || ci >= len(ws.Cols) {
		return
	}
	ws.Focus = ci
}

func (m *Mux) moveOverview(deltaCol, deltaPane int) {
	ws := m.curWS()
	if len(ws.Cols) == 0 {
		return
	}
	ws.OverviewCol = max(0, min(len(ws.Cols)-1, ws.OverviewCol+deltaCol))
	col := ws.Cols[ws.OverviewCol]
	ws.OverviewPane = max(0, min(len(col.Panes)-1, ws.OverviewPane+deltaPane))
}

func (m *Mux) selectOverview() {
	ws := m.curWS()
	m.focusPane(ws.OverviewCol, ws.OverviewPane)
	ws.Overview = false
}

func (m *Mux) handleHelpKey(k Key) bool {
	if k.Mouse != nil {
		if k.Mouse.WheelDelta != 0 {
			m.scrollHelp(k.Mouse.WheelDelta)
			return true
		}
		return false
	}
	if a, ok := m.cfg.keymap[k.Name]; ok && a.Name == "help" {
		m.run(a)
		return true
	}
	switch k.Name {
	case "esc":
		m.showHelp = false
	case "up":
		m.scrollHelp(-1)
	case "down":
		m.scrollHelp(1)
	case "pageup":
		m.scrollHelp(-m.helpPageSize())
	case "pagedown":
		m.scrollHelp(m.helpPageSize())
	case "home":
		m.helpOffset = 0
	case "end":
		m.helpOffset = m.helpMaxOffset()
	default:
		return false
	}
	return true
}

func (m *Mux) handleOverviewKey(k Key) bool {
	ws := m.curWS()
	if k.Mouse != nil {
		if k.Mouse.WheelDelta != 0 {
			m.moveOverview(0, k.Mouse.WheelDelta)
			return true
		}
		if k.Mouse.Left {
			if r, ok := m.hitTestOverview(k.Mouse.X, k.Mouse.Y); ok {
				ws.OverviewCol, ws.OverviewPane = r.col, r.pane
				m.selectOverview()
				return true
			}
		}
		return false
	}
	if a, ok := m.cfg.keymap[k.Name]; ok {
		switch a.Name {
		case "overview":
			m.run(a)
			return true
		case "focus_left":
			m.moveOverview(-1, 0)
			return true
		case "focus_right":
			m.moveOverview(1, 0)
			return true
		case "focus_up":
			m.moveOverview(0, -1)
			return true
		case "focus_down":
			m.moveOverview(0, 1)
			return true
		}
	}
	switch k.Name {
	case "left":
		m.moveOverview(-1, 0)
	case "right":
		m.moveOverview(1, 0)
	case "up":
		m.moveOverview(0, -1)
	case "down":
		m.moveOverview(0, 1)
	case "enter", "space":
		m.selectOverview()
	case "esc":
		ws.Overview = false
	default:
		return false
	}
	return true
}

func (m *Mux) handleMouse(k Key) bool {
	me := k.Mouse
	if me == nil {
		return false
	}
	ws := m.curWS()
	if m.showHelp {
		if me.WheelDelta != 0 {
			m.scrollHelp(me.WheelDelta)
		}
		return true
	}
	if ws.Overview {
		if me.WheelDelta != 0 {
			m.moveOverview(0, me.WheelDelta)
			return true
		}
		if me.Left {
			if r, ok := m.hitTestOverview(me.X, me.Y); ok {
				ws.OverviewCol, ws.OverviewPane = r.col, r.pane
				m.selectOverview()
				return true
			}
		}
		return true
	}
	if me.Left {
		if hit, ok := m.hitTestPanel(me.X, me.Y); ok {
			switch hit.kind {
			case "workspace":
				if hit.index >= 0 && hit.index < len(m.ws) {
					m.cur = hit.index
				}
			case "pane":
				m.focusColumn(hit.index)
			}
			return true
		}
		if r, ok := m.hitTestNormal(me.X, me.Y); ok {
			m.focusPane(r.col, r.pane)
		}
		return true
	}
	return false
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
		if k.Mouse != nil {
			flush()
			_ = m.handleMouse(k)
			continue
		}
		if m.showHelp {
			if m.handleHelpKey(k) {
				continue
			}
			continue
		}
		if m.curWS().Overview {
			if m.handleOverviewKey(k) {
				continue
			}
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
	tileH := m.contentH()
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
	if ws.ScrollMode == "center" {
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

func (m *Mux) normalPaneRects(ws *Workspace) []paneRect {
	var rects []paneRect
	tileH := m.contentH()
	fx := -ws.ViewX
	baseY := m.contentY()
	for ci, col := range ws.Cols {
		x0 := int(math.Round(fx))
		fx += col.CurW
		x1 := int(math.Round(fx))
		w := x1 - x0
		if w <= 0 || x1 <= 0 || x0 >= m.W {
			continue
		}
		y := baseY
		for pi := range col.Panes {
			h := paneHeight(tileH, len(col.Panes), pi)
			rects = append(rects, paneRect{col: ci, pane: pi, x: x0, y: y, w: w, h: h})
			y += h
		}
	}
	return rects
}

func splitSizes(total, parts int) []int {
	if parts <= 0 {
		return nil
	}
	s := make([]int, parts)
	for i := 0; i < parts; i++ {
		s[i] = total / parts
		if i < total%parts {
			s[i]++
		}
	}
	return s
}

func (m *Mux) overviewPaneRects(ws *Workspace) []paneRect {
	if len(ws.Cols) == 0 {
		return nil
	}
	innerX, innerY := 2, m.contentY()+1
	innerW, innerH := max(1, m.W-4), max(1, m.contentH()-2)
	total := 0
	widths := make([]int, len(ws.Cols))
	for i, c := range ws.Cols {
		widths[i] = max(6, m.targetW(c))
		total += widths[i]
	}
	if total <= 0 {
		return nil
	}
	used := 0
	var rects []paneRect
	x := innerX
	for ci, col := range ws.Cols {
		cw := (widths[ci] * innerW) / total
		if ci == len(ws.Cols)-1 {
			cw = innerW - used
		}
		cw = max(4, cw)
		used += cw
		heights := splitSizes(innerH, len(col.Panes))
		y := innerY
		for pi := range col.Panes {
			h := heights[pi]
			rects = append(rects, paneRect{col: ci, pane: pi, x: x, y: y, w: cw, h: h})
			y += h
		}
		x += cw
	}
	return rects
}

func (m *Mux) hitTestNormal(x, y int) (paneRect, bool) {
	for _, r := range m.normalPaneRects(m.curWS()) {
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return r, true
		}
	}
	return paneRect{}, false
}

func (m *Mux) hitTestOverview(x, y int) (paneRect, bool) {
	for _, r := range m.overviewPaneRects(m.curWS()) {
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return r, true
		}
	}
	return paneRect{}, false
}

func (m *Mux) hitTestPanel(x, y int) (panelHit, bool) {
	if y != m.panelY() {
		return panelHit{}, false
	}
	for _, h := range m.panelHits {
		if x >= h.x0 && x < h.x1 {
			return h, true
		}
	}
	return panelHit{}, false
}

func (m *Mux) drawOverview(scr *Screen) {
	ws := m.curWS()
	rects := m.overviewPaneRects(ws)
	if len(rects) == 0 {
		return
	}
	scr.Fill(0, m.contentY(), m.W, m.contentH(), Cell{R: ' ', FG: m.th.statusFG, BG: ColDefault})
	for _, r := range rects {
		if r.w < 2 || r.h < 2 {
			continue
		}
		focused := ws.Focus == r.col && ws.Cols[r.col].Focus == r.pane
		selected := ws.OverviewCol == r.col && ws.OverviewPane == r.pane
		fg := m.th.border
		bg := ColDefault
		attr := uint8(0)
		if focused {
			fg = m.th.accent
		}
		if selected {
			bg = m.th.statusBG
			attr = AttrBold
		}
		for yy := r.y; yy < r.y+r.h; yy++ {
			for xx := r.x; xx < r.x+r.w; xx++ {
				scr.Set(xx, yy, Cell{R: ' ', FG: fg, BG: bg, A: attr})
			}
		}
		if r.w > 2 && r.h > 2 {
			label := fmt.Sprintf("%d.%d", r.col+1, r.pane+1)
			scr.Text(r.x+1, r.y+r.h/2, truncate(label, r.w-2), fg, bg, attr)
		}
	}
	title := " overview — arrows/enter select, click to focus "
	scr.Text(max(0, (m.W-utf8.RuneCountInString(title))/2), m.contentY(), truncate(title, m.W), m.th.focusTitle, ColDefault, AttrBold)
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
	cx, cy, showCur := 0, 0, false
	tileH := m.contentH()
	if ws.Overview {
		m.drawOverview(scr)
	} else {
		for _, r := range m.normalPaneRects(ws) {
			p := ws.Cols[r.col].Panes[r.pane]
			focused := r.col == ws.Focus && r.pane == ws.Cols[r.col].Focus
			if x, yy, ok := m.drawPane(scr, p, r.x, r.y, r.w, r.h, focused); ok {
				cx, cy, showCur = x, yy, true
			}
		}
		if len(ws.Cols) == 0 {
			newPaneKey := m.cfg.primaryBinding("new_pane", "new pane key")
			msg := fmt.Sprintf("no panes — press %s to open a terminal", newPaneKey)
			scr.Text(max(0, (m.W-utf8.RuneCountInString(msg))/2), m.contentY()+tileH/2, msg, m.th.title, ColDefault, 0)
		}
	}
	m.drawStatus(scr)
	if m.showHelp {
		m.drawHelp(scr)
		showCur = false
	}
	m.rend.Flush(cx, cy, showCur)
}

func (m *Mux) drawStatus(scr *Screen) {
	y := m.panelY()
	scr.Fill(0, y, m.W, 1, Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	m.panelHits = nil
	metrics := statusMetrics{}
	if m.statusSampler != nil {
		metrics = m.statusSampler.Snapshot()
	}
	left := m.statusSection(m.cfg.Status.Left, metrics)
	center := m.statusSection(m.cfg.Status.Center, metrics)
	right := m.statusSection(m.cfg.Status.Right, metrics)
	if m.prefixPending {
		right = []statusSeg{{Text: " PREFIX ", FG: m.th.statusBG, BG: m.th.accent, A: AttrBold}}
	} else if m.msg != "" && time.Now().Before(m.msgUntil) {
		right = append([]statusSeg{{Text: " " + m.msg + " ", FG: m.th.statusFG, BG: m.th.statusBG}}, right...)
	}

	rightW := statusWidth(right)
	rightStart := max(0, m.W-rightW)
	leftEnd, leftHits := m.drawStatusSegments(scr, y, 0, rightStart, left)
	m.panelHits = append(m.panelHits, leftHits...)

	_, rightHits := m.drawStatusSegments(scr, y, rightStart, m.W, right)
	m.panelHits = append(m.panelHits, rightHits...)

	availStart, availEnd := leftEnd, rightStart
	if availStart < 0 {
		availStart = 0
	}
	if availEnd > m.W {
		availEnd = m.W
	}
	avail := availEnd - availStart
	if avail <= 0 {
		return
	}
	centerW := statusWidth(center)
	start := availStart + (avail-centerW)/2
	if start < availStart {
		start = availStart
	}
	_, centerHits := m.drawStatusSegments(scr, y, start, availEnd, center)
	m.panelHits = append(m.panelHits, centerHits...)
}

type statusSeg struct {
	Text  string
	FG, BG Col
	A     uint8
	Kind  string
	Index int
}

func statusWidth(segs []statusSeg) int {
	n := 0
	for _, s := range segs {
		n += utf8.RuneCountInString(s.Text)
	}
	return n
}

func (m *Mux) drawStatusSegments(scr *Screen, y, x, xMax int, segs []statusSeg) (int, []panelHit) {
	var hits []panelHit
	if x >= xMax {
		return x, hits
	}
	for _, seg := range segs {
		if x >= xMax {
			break
		}
		text := seg.Text
		if rem := xMax - x; utf8.RuneCountInString(text) > rem {
			text = truncate(text, rem)
		}
		if text == "" {
			break
		}
		start := x
		x = scr.Text(x, y, text, seg.FG, seg.BG, seg.A)
		if seg.Kind != "" {
			hits = append(hits, panelHit{x0: start, x1: x, kind: seg.Kind, index: seg.Index})
		}
	}
	return x, hits
}

func (m *Mux) statusSection(items []string, metrics statusMetrics) []statusSeg {
	ws := m.curWS()
	var out []statusSeg
	for _, item := range items {
		switch item {
		case "workspaces":
			for i := range m.ws {
				seg := statusSeg{
					Text:  fmt.Sprintf(" %d ", i+1),
					FG:    m.th.statusFG,
					BG:    m.th.statusBG,
					Kind:  "workspace",
					Index: i,
				}
				if i == m.cur {
					seg.FG, seg.BG, seg.A = m.th.statusBG, m.th.accent, AttrBold
				}
				out = append(out, seg)
			}
		case "panes":
			totalPanes := 0
			for _, c := range ws.Cols {
				totalPanes += len(c.Panes)
			}
			if totalPanes == 0 {
				out = append(out, statusSeg{Text: " -- ", FG: m.th.statusFG, BG: m.th.statusBG})
				continue
			}
			totalCols := len(ws.Cols)
			curCol := ws.Focus + 1
			var bar strings.Builder
			for i := 1; i <= totalCols; i++ {
				if i == curCol {
					bar.WriteString("█")
				} else {
					bar.WriteString("░")
				}
			}
			text := fmt.Sprintf(" %d/%d %s ", curCol, totalCols, bar.String())
			out = append(out, statusSeg{Text: text, FG: m.th.statusFG, BG: m.th.statusBG, Kind: "pane", Index: ws.Focus})
		case "cwd":
			cwd := paneCWD(m.focusedPane())
			if cwd == "" {
				cwd = "-"
			}
			out = append(out, statusSeg{Text: " cwd:" + cwd + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		case "battery":
			out = append(out, statusSeg{Text: " " + metrics.Battery + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		case "cpu":
			out = append(out, statusSeg{Text: " " + metrics.CPU + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		case "memory":
			out = append(out, statusSeg{Text: " " + metrics.Memory + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		case "load":
			out = append(out, statusSeg{Text: " " + metrics.Load + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		case "time":
			out = append(out, statusSeg{Text: " " + time.Now().Format("15:04") + " ", FG: m.th.statusFG, BG: m.th.statusBG})
		}
	}
	return out
}

func paneCWD(p *Pane) string {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return ""
	}
	link := fmt.Sprintf("/proc/%d/cwd", p.Cmd.Process.Pid)
	cwd, err := os.Readlink(link)
	if err != nil {
		return p.Cmd.Dir
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		if cwd == home {
			return "~"
		}
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}

func (m *Mux) helpPageSize() int {
	return max(1, m.contentH()-4)
}

func (m *Mux) helpMaxOffset() int {
	lines := len(m.cfg.helpLines())
	return max(0, lines-m.helpPageSize())
}

func (m *Mux) scrollHelp(delta int) {
	m.helpOffset = max(0, min(m.helpOffset+delta, m.helpMaxOffset()))
}

func (m *Mux) drawHelp(scr *Screen) {
	lines := m.cfg.helpLines()
	maxLen := 0
	for _, l := range lines {
		maxLen = max(maxLen, utf8.RuneCountInString(l))
	}
	title := fmt.Sprintf(" shortcuts — %s toggles ", m.cfg.primaryBinding("help", "help"))
	maxLen = max(maxLen, utf8.RuneCountInString(title))
	w := min(maxLen+4, m.W)
	avail := m.contentH() - 2
	h := max(3, min(max(avail, 3), len(lines)+2))
	x := (m.W - w) / 2
	y := m.contentY() + (m.contentH()-h)/2
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
	viewport := max(1, h-2)
	maxOff := max(0, len(lines)-viewport)
	m.helpOffset = max(0, min(m.helpOffset, maxOff))
	end := min(len(lines), m.helpOffset+viewport)
	visible := lines[m.helpOffset:end]
	if len(lines) > viewport {
		title = fmt.Sprintf("%s [%d-%d/%d]", title, m.helpOffset+1, end, len(lines))
	}
	scr.Text(x+2, y, truncate(title, w-4), m.th.focusTitle, m.th.statusBG, AttrBold)
	for i, l := range visible {
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
