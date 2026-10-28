// Package mux is the multiplexer itself: workspaces of scrolling columns of
// panes, plus everything drawn on top (status bar, help, overview, selection).
//
// File map:
//
//	mux.go        types, construction, main event loop
//	layout.go     columns / panes / workspaces, scrolling geometry, animation
//	actions.go    what each bound action does
//	keyboard.go   key dispatch (shortcuts, help, overview, prefix)
//	mouse.go      clicks, drag-selection, wheel, forwarding to apps
//	render.go     composing a frame (panes, overview thumbnails)
//	bar.go        status bar and its widgets
//	help.go       the scrollable help overlay
//	selection.go  selection model + text extraction
//	copymode.go   modal keyboard copy mode
package mux

import (
	"bufio"
	"math"
	"strings"
	"time"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/pane"
	"ananta/internal/screen"
	"ananta/internal/stats"
)

const (
	minColW       = 12
	numWorkspaces = 9
)

type Column struct {
	Panes     []*pane.Pane
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
	border, focusBorder, title, focusTitle   screen.Col
	statusBG, statusFG, accent, selBG, selFG screen.Col
}

// hit records where a pane was drawn last frame, for mouse hit-testing.
type hit struct {
	p          *pane.Pane
	ws, ci, pi int
	x, y, w, h int
	thumb      bool
}

type tabHit struct{ x0, x1, ws int }

type Mux struct {
	cfg    *config.Config
	th     theme
	bs     config.BorderSet
	ws     []*Workspace
	cur    int
	W, H   int
	rend   *screen.Renderer
	nextID int

	dirty chan struct{}
	exits chan *pane.Pane
	stats *stats.Collector

	scrollMode string // "fit" | "center", toggled at runtime
	overview   bool
	scale      float64 // animated zoom: 1 = normal, cfg.OverviewScale = overview
	ovY        float64 // animated vertical offset across workspace rows

	prefixPending bool
	showHelp      bool
	helpScroll    int
	helpVis       int
	quit          bool
	barHidden     bool
	skipRelease   bool
	msg           string
	msgUntil      time.Time

	hits []hit
	tabs []tabHit

	sel         *Selection // mouse selection (highlighted until next input)
	selDrag     bool
	cm          *CopyMode   // non-nil while in copy mode
	clipCh      chan string // OSC 52 requests from programs running in panes
	startDir    string      // where panes start when there is no focused pane to copy from
	session     string      // name shown by the "session" bar widget ("" in local mode)
	wall        *pane.Pane  // wallpaper (not part of any workspace); nil when disabled
	wallRestart chan struct{}

	attached bool // false while a session has no client: panes keep running, nothing is drawn

	// OnDetach is set by the session server; the detach action calls it.
	OnDetach func()
}

func New(cfg *config.Config, out *bufio.Writer) *Mux {
	m := &Mux{
		cfg:         cfg,
		bs:          config.BorderFor(cfg.BorderStyle),
		rend:        screen.NewRenderer(out),
		dirty:       make(chan struct{}, 1),
		exits:       make(chan *pane.Pane, 64),
		clipCh:      make(chan string, 8),
		wallRestart: make(chan struct{}, 1),
		stats:       stats.New(),
		scrollMode:  cfg.ScrollMode,
		scale:       1,
		attached:    true,
		th: theme{
			border: config.ParseColor(cfg.Theme.Border), focusBorder: config.ParseColor(cfg.Theme.FocusBorder),
			title: config.ParseColor(cfg.Theme.Title), focusTitle: config.ParseColor(cfg.Theme.FocusTitle),
			statusBG: config.ParseColor(cfg.Theme.StatusBG), statusFG: config.ParseColor(cfg.Theme.StatusFG),
			accent: config.ParseColor(cfg.Theme.Accent),
			selBG:  config.ParseColor(cfg.Theme.SelectionBG), selFG: config.ParseColor(cfg.Theme.SelectionFG),
		},
	}
	for i := 0; i < numWorkspaces; i++ {
		m.ws = append(m.ws, &Workspace{})
	}
	warn := append([]string(nil), cfg.Warnings...)
	if cfg.LeaderMask&input.ModSuper != 0 {
		warn = append(warn, "leader=super needs a terminal with the extended keyboard protocol")
	}
	if len(warn) > 0 {
		m.notify(strings.Join(warn, "; "))
	}
	return m
}

// SetSize tells the mux the terminal size (call before Start and on resize).
func (m *Mux) SetSize(w, h int) {
	if w < 1 || h < 2 {
		return
	}
	m.W, m.H = w, h
	m.rend.Resize(w, h)
}

// Start launches the stats sampler and the wallpaper. No pane is opened: the
// user opens the first one (new_pane) and ananta only exits on quit.
func (m *Mux) Start() {
	custom := map[string]stats.Custom{}
	for name, w := range m.cfg.Bar.Widgets {
		if w.Command != "" {
			custom[name] = stats.Custom{Command: w.Command, Interval: time.Duration(w.Interval * float64(time.Second))}
		}
	}
	m.stats.Start(time.Duration(m.cfg.Bar.Interval*float64(time.Second)), custom, func() {
		select {
		case m.dirty <- struct{}{}:
		default:
		}
	})
	m.startWallpaper()
}

// Shutdown stops background work and hangs up every pane.
func (m *Mux) Shutdown() {
	m.stats.Stop()
	if m.wall != nil {
		m.wall.Close()
	}
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				p.Close()
			}
		}
	}
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

func (m *Mux) focusedPane() *pane.Pane {
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

// SetAttached turns drawing on or off (a detached session keeps running but
// has nobody to draw for). Attaching forces a full repaint.
func (m *Mux) SetAttached(on bool) {
	if on && !m.attached {
		m.rend.Resize(m.W, m.H)
	}
	m.attached = on
}

// SetSessionName sets the name shown by the "session" bar widget.
func (m *Mux) SetSessionName(name string) { m.session = name }

// SetStartDir sets the directory new panes start in when there is no focused
// pane whose directory can be inherited.
func (m *Mux) SetStartDir(dir string) { m.startDir = dir }

// Quit ends the main loop.
func (m *Mux) Quit() { m.quit = true }

// Quitting reports whether Quit has been requested.
func (m *Mux) Quitting() bool { return m.quit }

// PaneCount reports the number of live panes (call from the loop goroutine).
func (m *Mux) PaneCount() int { return m.totalPanes() }

// Loop runs until quit. Everything that touches mux state runs on this
// goroutine: keys arrive on keys, and other goroutines hand it work (resize,
// attach, detach, ...) as closures on post.
func (m *Mux) Loop(keys <-chan []input.Key, post <-chan func()) {
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
		case fn := <-post:
			fn()
			need = true
		case <-m.dirty:
			need = true
		case text := <-m.clipCh:
			m.copyText(text)
			need = true
		case p := <-m.exits:
			if p == m.wall {
				m.wallpaperExited(p)
			} else {
				m.removePane(p)
			}
			need = true
		case <-m.wallRestart:
			m.startWallpaper()
			need = true
		case now := <-tick.C:
			dt := math.Min(now.Sub(last).Seconds(), 0.1)
			last = now
			animating := m.step(dt)
			if m.msg != "" && !time.Now().Before(m.msgUntil) {
				m.msg = ""
				need = true
			}
			if m.attached && (need || animating) {
				m.render()
				need = animating
			}
		}
	}
}
