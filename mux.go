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
	CurW      float64 // animated, displayed width in cells (unscaled)
}

type Workspace struct {
	Cols    []*Column
	Focus   int
	ViewX   float64 // animated viewport offset into the strip (unscaled cells)
	TargetX float64
}

type theme struct {
	border, focusBorder, title, focusTitle, statusBG, statusFG, accent Col
}

type hit struct {
	p          *Pane
	ws, ci, pi int
	x, y, w, h int
	thumb      bool
}

type tabHit struct{ x0, x1, ws int }

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

	scrollMode string // "fit" | "center", toggled at runtime
	overview   bool
	scale      float64 // animated zoom: 1 = normal, cfg.OverviewScale = overview
	ovY        float64 // animated vertical offset across workspace rows

	prefixPending bool
	showHelp      bool
	helpScroll    int
	helpVis       int
	quit          bool
	skipRelease   bool
	msg           string
	msgUntil      time.Time

	hits []hit
	tabs []tabHit
}

func removeAt[T any](s []T, i int) []T { return append(s[:i], s[i+1:]...) }

func newMux(cfg *Config, out *bufio.Writer) *Mux {
	m := &Mux{
		cfg:        cfg,
		bs:         borderFor(cfg.BorderStyle),
		rend:       NewRenderer(out),
		dirty:      make(chan struct{}, 1),
		exits:      make(chan *Pane, 64),
		scrollMode: cfg.ScrollMode,
		scale:      1,
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
	warn := cfg.warnings
	if cfg.leaderMask&modSuper != 0 {
		warn = append(warn, "leader=super needs a kitty-keyboard-protocol terminal")
	}
	if len(warn) > 0 {
		m.notify(strings.Join(warn, "; "))
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

// visibleWS lists the workspaces drawn as rows: non-empty ones plus the current.
func (m *Mux) visibleWS() []int {
	var v []int
	for i, ws := range m.ws {
		if i == m.cur || len(ws.Cols) > 0 {
			v = append(v, i)
		}
	}
	return v
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

func (m *Mux) findPane(p *Pane) (ws *Workspace, wi, ci, pi int, ok bool) {
	for w, wsp := range m.ws {
		for c, col := range wsp.Cols {
			for i, q := range col.Panes {
				if q == p {
					return wsp, w, c, i, true
				}
			}
		}
	}
	return nil, 0, 0, 0, false
}

func (m *Mux) removePane(p *Pane) {
	p.Close()
	if ws, _, ci, pi, ok := m.findPane(p); ok {
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

// ---------------------------------------------------------------- scrolling geometry

func (m *Mux) geom(ws *Workspace) (total, fx, fw float64) {
	for i, c := range ws.Cols {
		w := float64(m.targetW(c))
		if i == ws.Focus {
			fx, fw = total, w
		}
		total += w
	}
	return
}

func (m *Mux) centerNow(ws *Workspace) {
	if len(ws.Cols) == 0 {
		return
	}
	_, fx, fw := m.geom(ws)
	ws.TargetX = fx + fw/2 - float64(m.W)/2
}

// updateTarget keeps ws.TargetX (the normal-mode scroll target) up to date.
func (m *Mux) updateTarget(ws *Workspace) {
	if len(ws.Cols) == 0 {
		ws.TargetX = 0
		return
	}
	total, fx, fw := m.geom(ws)
	W := float64(m.W)
	if m.scrollMode == "center" {
		ws.TargetX = fx + fw/2 - W/2 // always centered, like niri's "always"
		return
	}
	// "fit" (PaperWM-like): move only as far as needed to show the column
	if fw >= W || fx < ws.TargetX {
		ws.TargetX = fx
	} else if fx+fw > ws.TargetX+W {
		ws.TargetX = fx + fw - W
	}
	ws.TargetX = math.Max(0, math.Min(ws.TargetX, math.Max(0, total-W)))
}

func (m *Mux) effTarget(ws *Workspace) float64 {
	if !m.overview || len(ws.Cols) == 0 {
		return ws.TargetX
	}
	z := m.cfg.OverviewScale
	Wv := float64(m.W) / z // viewport width in unscaled cells
	total, fx, fw := m.geom(ws)
	if total <= Wv {
		return (total - Wv) / 2 // whole strip fits: center it
	}
	return math.Max(0, math.Min(fx+fw/2-Wv/2, total-Wv))
}

func (m *Mux) gap() float64 {
	z := m.cfg.OverviewScale
	return math.Max(0, math.Min(1, (1-m.scale)/(1-z)))
}

func (m *Mux) rowGeom() (rowHf, pitch float64) {
	rowHf = float64(m.H-1) * m.scale
	return rowHf, rowHf + m.gap()
}

// ---------------------------------------------------------------- actions

func (m *Mux) moveWorkspaceRel(d int) {
	vis := m.visibleWS()
	for i, w := range vis {
		if w == m.cur {
			m.cur = vis[max(0, min(len(vis)-1, i+d))]
			return
		}
	}
}

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
	case "center_column":
		m.centerNow(ws)
	case "center_mode":
		if m.scrollMode == "center" {
			m.scrollMode = "fit"
			m.notify("scrolling: PaperWM style (fit)")
		} else {
			m.scrollMode = "center"
			m.notify("scrolling: always centered")
		}
	case "overview":
		m.overview = !m.overview
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
		m.helpScroll = 0
	case "quit":
		m.quit = true
	}
}

// ---------------------------------------------------------------- keyboard input

func (m *Mux) appCursor(p *Pane) bool {
	p.Term.Lock()
	defer p.Term.Unlock()
	return p.Term.Mode()&vt10x.ModeAppCursor != 0
}

func (m *Mux) translate(k Key) []byte {
	r := k.Raw
	if len(r) == 3 && r[0] == 0x1b && r[1] == '[' && strings.IndexByte("ABCDHF", r[2]) >= 0 {
		if p := m.focusedPane(); p != nil && m.appCursor(p) {
			return []byte{0x1b, 'O', r[2]}
		}
	}
	return r
}

func (m *Mux) helpKey(k Key) {
	if a, ok := m.cfg.keymap[k.Name]; ok && a.Name == "help" {
		m.showHelp = false
		return
	}
	page := max(1, m.helpVis-1)
	switch k.Name {
	case "esc", "q", "enter":
		m.showHelp = false
	case "down", "j", "ctrl+n":
		m.helpScroll++
	case "up", "k", "ctrl+p":
		m.helpScroll--
	case "pagedown", "space", "ctrl+d", "ctrl+f":
		m.helpScroll += page
	case "pageup", "ctrl+u", "ctrl+b", "b":
		m.helpScroll -= page
	case "home", "g":
		m.helpScroll = 0
	case "end", "shift+g":
		m.helpScroll = 1 << 20
	}
}

func (m *Mux) overviewKey(k Key) bool {
	switch k.Name {
	case "esc":
		m.overview = false
	case "enter", "space":
		m.overview = false
		m.centerNow(m.curWS())
	case "left", "h":
		m.run(Action{Name: "focus_left"})
	case "right", "l":
		m.run(Action{Name: "focus_right"})
	case "up", "k":
		if c := m.focusedCol(); c != nil && c.Focus > 0 {
			c.Focus--
		} else {
			m.moveWorkspaceRel(-1)
		}
	case "down", "j":
		if c := m.focusedCol(); c != nil && c.Focus < len(c.Panes)-1 {
			c.Focus++
		} else {
			m.moveWorkspaceRel(1)
		}
	default:
		return false
	}
	return true
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
			if m.cfg.Mouse {
				m.handleMouse(k.Mouse)
			}
			continue
		}
		if m.showHelp {
			m.helpKey(k)
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
		if m.overview {
			m.overviewKey(k) // everything else is swallowed in the overview
			continue
		}
		pending = append(pending, m.translate(k)...)
	}
	flush()
}

// ---------------------------------------------------------------- mouse

func (m *Mux) hitAt(x, y int) *hit {
	for i := len(m.hits) - 1; i >= 0; i-- {
		h := m.hits[i]
		if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
			return &h
		}
	}
	return nil
}

func (m *Mux) isFocused(h *hit) bool {
	if h.ws != m.cur {
		return false
	}
	ws := m.curWS()
	return h.ci == ws.Focus && h.ci < len(ws.Cols) && h.pi == ws.Cols[h.ci].Focus
}

func (m *Mux) focusHit(h *hit) {
	ws, wi, ci, pi, ok := m.findPane(h.p)
	if !ok {
		return
	}
	m.cur = wi
	ws.Focus = ci
	ws.Cols[ci].Focus = pi
}

func (m *Mux) forwardMouse(h *hit, ev *Mouse) {
	if h.thumb {
		return
	}
	p := h.p
	p.Term.Lock()
	mode := p.Term.Mode()
	p.Term.Unlock()
	if mode&vt10x.ModeMouseMask == 0 {
		return
	}
	if ev.Btn&32 != 0 && mode&(vt10x.ModeMouseMotion|vt10x.ModeMouseMany) == 0 {
		return
	}
	cx, cy := ev.X-h.x, ev.Y-h.y // inner coords are 1-based: (x-(h.x+1))+1
	if cx < 1 || cy < 1 || cx > h.w-2 || cy > h.h-2 {
		return
	}
	var b []byte
	if mode&vt10x.ModeMouseSgr != 0 {
		final := 'M'
		if !ev.Press {
			final = 'm'
		}
		b = fmt.Appendf(nil, "\x1b[<%d;%d;%d%c", ev.Btn, cx, cy, final)
	} else {
		if cx > 222 || cy > 222 {
			return
		}
		code := ev.Btn
		if !ev.Press {
			code = (code &^ 3) | 3
		}
		b = []byte{0x1b, '[', 'M', byte(32 + code), byte(32 + cx), byte(32 + cy)}
	}
	_, _ = p.PTY.Write(b)
}

func (m *Mux) handleWheel(ev *Mouse) {
	dir := ev.Btn & 3 // 0 up, 1 down, 2 left, 3 right
	if m.overview {
		switch dir {
		case 0:
			m.moveWorkspaceRel(-1)
		case 1:
			m.moveWorkspaceRel(1)
		case 2:
			m.run(Action{Name: "focus_left"})
		case 3:
			m.run(Action{Name: "focus_right"})
		}
		return
	}
	h := m.hitAt(ev.X, ev.Y)
	if h == nil {
		return
	}
	h.p.Term.Lock()
	mode := h.p.Term.Mode()
	h.p.Term.Unlock()
	switch {
	case mode&vt10x.ModeMouseMask != 0:
		m.forwardMouse(h, ev)
	case dir >= 2:
		if dir == 2 {
			m.run(Action{Name: "focus_left"})
		} else {
			m.run(Action{Name: "focus_right"})
		}
	case mode&vt10x.ModeAltScreen != 0: // full-screen app without mouse: wheel -> arrows
		c := byte('A')
		if dir == 1 {
			c = 'B'
		}
		intro := []byte{0x1b, '['}
		if mode&vt10x.ModeAppCursor != 0 {
			intro = []byte{0x1b, 'O'}
		}
		seq := append(intro, c)
		_, _ = h.p.PTY.Write(append(append(append([]byte{}, seq...), seq...), seq...))
	}
}

func (m *Mux) handleMouse(ev *Mouse) {
	wheel := ev.Btn&64 != 0
	if m.showHelp {
		if wheel {
			switch ev.Btn & 3 {
			case 0:
				m.helpScroll -= 3
			case 1:
				m.helpScroll += 3
			}
		}
		return
	}
	if wheel {
		m.handleWheel(ev)
		return
	}
	if !ev.Press && ev.Btn&32 == 0 && m.skipRelease {
		m.skipRelease = false
		return
	}
	leftPress := ev.Press && ev.Btn&3 == 0 && ev.Btn&32 == 0
	if leftPress {
		if ev.Y == m.H-1 {
			for _, t := range m.tabs {
				if ev.X >= t.x0 && ev.X < t.x1 {
					m.cur = t.ws
				}
			}
			return
		}
		h := m.hitAt(ev.X, ev.Y)
		if h == nil {
			return
		}
		if m.overview {
			m.focusHit(h)
			m.overview = false
			m.centerNow(m.curWS()) // clicked window glides to the center
			m.skipRelease = true
			return
		}
		if m.isFocused(h) {
			m.forwardMouse(h, ev)
			return
		}
		m.focusHit(h)
		m.skipRelease = true
		return
	}
	if m.overview {
		return
	}
	if p := m.focusedPane(); p != nil {
		for i := range m.hits {
			if m.hits[i].p == p {
				m.forwardMouse(&m.hits[i], ev)
				break
			}
		}
	}
}

// ---------------------------------------------------------------- animation

// syncSizes resizes every PTY to the *target* layout so apps reflow once,
// while the on-screen rectangle animates towards it (the overview never
// resizes panes: it shows scaled-down snapshots of the real content).
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

func (m *Mux) step(dt float64) bool {
	alpha := 1.0
	if m.cfg.Animate {
		alpha = 1 - math.Exp(-m.cfg.AnimationSpeed*dt)
	}
	animating := false
	ease := func(cur *float64, target, eps, minStep float64) {
		d := target - *cur
		if math.Abs(d) < eps || alpha >= 1 {
			*cur = target
			return
		}
		mv := d * alpha
		if math.Abs(mv) < minStep {
			mv = math.Copysign(math.Min(minStep, math.Abs(d)), d)
		}
		*cur += mv
		animating = true
	}
	ts := 1.0
	if m.overview {
		ts = m.cfg.OverviewScale
	}
	ease(&m.scale, ts, 0.004, 0.02)
	for _, ws := range m.ws {
		m.updateTarget(ws)
		for _, c := range ws.Cols {
			ease(&c.CurW, float64(m.targetW(c)), 0.05, 0.6)
		}
		ease(&ws.ViewX, m.effTarget(ws), 0.05, 0.6)
	}
	vis := m.visibleWS()
	idx := 0
	for i, w := range vis {
		if w == m.cur {
			idx = i
		}
	}
	rowHf, pitch := m.rowGeom()
	ease(&m.ovY, float64(idx)*pitch-(float64(m.H-1)-rowHf)/2, 0.05, 0.6)
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

func (m *Mux) drawBox(scr *Screen, x, y, w, h int, bc Col, bg Col) {
	bs := m.bs
	cell := func(r rune) Cell { return Cell{R: r, FG: bc, BG: bg} }
	scr.Set(x, y, cell(bs.tl))
	scr.Set(x+w-1, y, cell(bs.tr))
	scr.Set(x, y+h-1, cell(bs.bl))
	scr.Set(x+w-1, y+h-1, cell(bs.br))
	for i := 1; i < w-1; i++ {
		scr.Set(x+i, y, cell(bs.h))
		scr.Set(x+i, y+h-1, cell(bs.h))
	}
	for j := 1; j < h-1; j++ {
		scr.Set(x, y+j, cell(bs.v))
		scr.Set(x+w-1, y+j, cell(bs.v))
	}
}

func (m *Mux) drawPane(scr *Screen, p *Pane, x, y, w, h int, focused, thumb bool) (cx, cy int, cursor bool) {
	if w < 2 || h < 2 {
		return
	}
	bc, tc, ta := m.th.border, m.th.title, uint8(0)
	if focused {
		bc, tc, ta = m.th.focusBorder, m.th.focusTitle, AttrBold
	}
	m.drawBox(scr, x, y, w, h, bc, ColDefault)

	iw, ih := w-2, h-2
	p.Term.Lock()
	title := p.Term.Title()
	tcols, trows := p.Term.Size()
	if !thumb {
		for row := 0; row < ih; row++ {
			for col := 0; col < iw; col++ {
				if col < tcols && row < trows {
					scr.Set(x+1+col, y+1+row, glyphCell(p.Term.Cell(col, row)))
				} else {
					scr.Set(x+1+col, y+1+row, blank)
				}
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
			scr.Text(x+2, y, " "+t+" ", tc, ColDefault, ta)
		}
	}
	if !thumb && focused && visible && cur.X < iw && cur.Y < ih && cur.X < tcols && cur.Y < trows {
		return x + 1 + cur.X, y + 1 + cur.Y, true
	}
	return
}

func (m *Mux) render() {
	m.syncSizes()
	scr := m.rend.cur
	scr.Clear()
	m.hits = m.hits[:0]
	m.tabs = m.tabs[:0]

	s := m.scale
	thumb := s < 0.995
	tileH := m.H - 1
	rowHf, pitch := m.rowGeom()
	rowH := tileH
	if thumb {
		rowH = max(4, int(math.Round(rowHf)))
	}
	showLabels := m.gap() > 0.9
	cx, cy, showCur := 0, 0, false

	for k, wi := range m.visibleWS() {
		ws := m.ws[wi]
		top := int(math.Round(float64(k)*pitch - m.ovY))
		if top >= tileH || top+rowH <= 0 {
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
					x >= 0 && x < m.W && yy >= 0 && yy < tileH {
					cx, cy, showCur = x, yy, true
				}
				m.hits = append(m.hits, hit{p: p, ws: wi, ci: ci, pi: pi, x: x0, y: y, w: w, h: h, thumb: thumb})
				y += h
			}
		}
		if len(ws.Cols) == 0 {
			msg := "empty workspace — open a terminal"
			if key := m.cfg.keyFor("new_pane"); key != "" {
				msg = "empty workspace — press " + key + " to open a terminal"
			}
			scr.Text(max(0, (m.W-utf8.RuneCountInString(msg))/2), top+rowH/2, truncate(msg, m.W), m.th.title, ColDefault, 0)
		}
		if showLabels && top-1 >= 0 {
			fg, bg := m.th.statusFG, m.th.statusBG
			if wi == m.cur {
				fg, bg = m.th.statusBG, m.th.accent
			}
			scr.Text(1, top-1, fmt.Sprintf(" %d ", wi+1), fg, bg, AttrBold)
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
	y := m.H - 1
	st := Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG}
	scr.Fill(0, y, m.W, 1, st)
	x := 1
	for i, ws := range m.ws {
		if i != m.cur && len(ws.Cols) == 0 {
			continue
		}
		label := fmt.Sprintf(" %d ", i+1)
		x0 := x
		if i == m.cur {
			x = scr.Text(x, y, label, m.th.statusBG, m.th.accent, AttrBold)
		} else {
			x = scr.Text(x, y, label, m.th.statusFG, m.th.statusBG, 0)
		}
		m.tabs = append(m.tabs, tabHit{x0: x0, x1: x, ws: i})
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
	if m.scrollMode == "center" {
		x = scr.Text(x+2, y, "centered", m.th.accent, m.th.statusBG, 0)
	}
	if m.overview {
		x = scr.Text(x+2, y, " OVERVIEW ", m.th.statusBG, m.th.accent, AttrBold)
	}
	right := ""
	if k := m.cfg.keyFor("help"); k != "" {
		right = k + " help "
	}
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
	title := fmt.Sprintf(" shortcuts · leader = %s ", m.cfg.Leader)
	foot := " j/k pgup/pgdn scroll · esc close "
	maxLen := max(utf8.RuneCountInString(title), utf8.RuneCountInString(foot)+8)
	for _, l := range lines {
		maxLen = max(maxLen, utf8.RuneCountInString(l))
	}
	w := min(maxLen+5, m.W)
	vis := max(1, min(len(lines), m.H-1-4))
	m.helpVis = vis
	maxScroll := max(0, len(lines)-vis)
	m.helpScroll = max(0, min(m.helpScroll, maxScroll))
	h := vis + 2
	x, y := (m.W-w)/2, max(0, (m.H-1-h)/2)
	scr.Fill(x, y, w, h, Cell{R: ' ', FG: m.th.statusFG, BG: m.th.statusBG})
	m.drawBox(scr, x, y, w, h, m.th.focusBorder, m.th.statusBG)
	scr.Text(x+2, y, truncate(title, w-4), m.th.focusTitle, m.th.statusBG, AttrBold)
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
			scr.Set(x+w-2, y+1+i, Cell{R: r, FG: fg, BG: m.th.statusBG})
		}
		counter := fmt.Sprintf(" %d-%d/%d ", m.helpScroll+1, m.helpScroll+vis, len(lines))
		scr.Text(x+w-2-utf8.RuneCountInString(counter), y+h-1, counter, m.th.statusFG, m.th.statusBG, 0)
	}
	scr.Text(x+2, y+h-1, truncate(foot, w-4), m.th.statusFG, m.th.statusBG, 0)
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
