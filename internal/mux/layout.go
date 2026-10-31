package mux

import (
	"fmt"
	"math"

	"ananta/internal/pane"
)

// ---- tile area (everything except the status bar)

func (m *Mux) barVisible() bool { return m.cfg.Bar.Position != "hidden" && !m.barHidden }

func (m *Mux) tileTop() int {
	if m.barVisible() && m.cfg.Bar.Position == "top" {
		return 1
	}
	return 0
}

func (m *Mux) tileH() int {
	if m.barVisible() {
		return m.H - 1
	}
	return m.H
}

func (m *Mux) barY() int {
	if m.cfg.Bar.Position == "top" {
		return 0
	}
	return m.H - 1
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

// ---- structure

func removeAt[T any](s []T, i int) []T { return append(s[:i], s[i+1:]...) }

// detachAt removes a pane from its column (dropping the column when it
// empties) and fixes focus indices. The pane itself is not closed.
func (ws *Workspace) detachAt(ci, pi int) *pane.Pane {
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

// newColumnFor puts p in a new column to the right of ws's focused column.
// frac is the column width as a fraction of the screen (<= 0: the default).
func (m *Mux) newColumnFor(ws *Workspace, p *pane.Pane, frac float64) {
	if frac <= 0 {
		frac = m.cfg.DefaultColumnWidth
	}
	col := &Column{Panes: []*pane.Pane{p}, WFrac: frac}
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

// spawn starts a pane for an action: it begins where the focused pane is.
func (m *Mux) spawn(cmdline string) (*pane.Pane, error) {
	dir := m.startDir
	if fp := m.focusedPane(); fp != nil {
		if d := fp.Cwd(); d != "" {
			dir = d
		}
	}
	return m.spawnWith(cmdline, dir, m.cfg.DefaultColumnWidth)
}

// spawnWith starts a pane in dir, sized for a column of width frac (a
// fraction of the screen width).
func (m *Mux) spawnWith(cmdline, dir string, frac float64) (*pane.Pane, error) {
	m.nextID++
	var clip chan<- string
	if m.cfg.AppClipboard {
		clip = m.clipCh
	}
	p, err := pane.New(pane.Options{
		ID: m.nextID, Shell: m.cfg.Shell, Cmdline: cmdline, Dir: dir,
		Cols: int(frac*float64(m.W)) - 2, Rows: m.tileH() - 2, Scrollback: m.cfg.ScrollbackLines,
		Env: m.paneEnv(m.nextID),
	}, m.dirty, m.exits, clip)
	if err == nil {
		m.applyHostColors(p)
	}
	return p, err
}

// paneEnv is what lets programs (and the `ananta` command run inside a pane)
// find the session and pane they belong to.
func (m *Mux) paneEnv(id int) []string {
	env := []string{fmt.Sprintf("ANANTA_PANE=%d", id)}
	if m.session != "" {
		env = append(env, "ANANTA_SESSION="+m.session)
	}
	return env
}

func (m *Mux) newPane(cmdline string) {
	p, err := m.spawn(cmdline)
	if err != nil {
		m.notify("spawn failed: " + err.Error())
		return
	}
	m.newColumnFor(m.curWS(), p, 0)
}

func (m *Mux) newPaneBelow(cmdline string) {
	if m.focusedCol() == nil {
		m.newPane(cmdline)
		return
	}
	p, err := m.spawn(cmdline)
	if err != nil {
		m.notify("spawn failed: " + err.Error())
		return
	}
	m.stackBelow(m.curWS(), p)
}

// stackBelow adds p under the focused pane of ws's focused column.
func (m *Mux) stackBelow(ws *Workspace, p *pane.Pane) {
	col := ws.Cols[ws.Focus]
	i := col.Focus + 1
	col.Panes = append(col.Panes, nil)
	copy(col.Panes[i+1:], col.Panes[i:])
	col.Panes[i] = p
	col.Focus = i
}

func (m *Mux) findPane(p *pane.Pane) (ws *Workspace, wi, ci, pi int, ok bool) {
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

func (m *Mux) removePane(p *pane.Pane) {
	p.Close()
	if m.cm != nil && m.cm.P == p {
		m.cm = nil
	}
	if m.sel != nil && m.sel.P == p {
		m.sel, m.selDrag = nil, false
	}
	if ws, _, ci, pi, ok := m.findPane(p); ok {
		ws.detachAt(ci, pi)
	}
}

func (m *Mux) moveWorkspaceRel(d int) {
	vis := m.visibleWS()
	for i, w := range vis {
		if w == m.cur {
			m.cur = vis[max(0, min(len(vis)-1, i+d))]
			return
		}
	}
}

// ---- scrolling geometry

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
		ws.TargetX = fx + fw/2 - W/2 // always centered
		return
	}
	// "fit": move only as far as needed to show the column
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
	rowHf = float64(m.tileH()) * m.scale
	return rowHf, rowHf + m.gap()
}

// ---- sizing + animation

// syncSizes resizes every PTY to the *target* layout so apps reflow once,
// while the on-screen rectangle animates towards it (the overview never
// resizes panes: it shows scaled-down snapshots of the real content).
func (m *Mux) syncSizes() {
	tileH := m.tileH()
	if m.wall != nil {
		m.wall.Resize(m.W, tileH)
	}
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
	ease(&m.ovY, float64(idx)*pitch-(float64(m.tileH())-rowHf)/2, 0.05, 0.6)
	return animating
}
