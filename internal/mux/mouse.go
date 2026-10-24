package mux

import (
	"fmt"

	"github.com/hinshun/vt10x"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/pane"
)

func (m *Mux) hitAt(x, y int) *hit {
	for i := len(m.hits) - 1; i >= 0; i-- {
		h := m.hits[i]
		if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
			return &h
		}
	}
	return nil
}

func (m *Mux) hitFor(p *pane.Pane) *hit {
	for i := len(m.hits) - 1; i >= 0; i-- {
		if m.hits[i].p == p && !m.hits[i].thumb {
			return &m.hits[i]
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

// innerPos converts a screen position to pane cell coordinates.
func innerPos(h *hit, x, y int) (cx, cy int, ok bool) {
	cx, cy = x-(h.x+1), y-(h.y+1)
	return cx, cy, cx >= 0 && cy >= 0 && cx < h.w-2 && cy < h.h-2
}

func paneMode(p *pane.Pane) vt10x.ModeFlag {
	p.Term.Lock()
	defer p.Term.Unlock()
	return p.Term.Mode()
}

// forwardMouse re-encodes a mouse event relative to the pane and sends it to
// the app, but only if the app asked for mouse reporting.
func (m *Mux) forwardMouse(h *hit, ev *input.Mouse) {
	if h.thumb {
		return
	}
	mode := paneMode(h.p)
	if mode&vt10x.ModeMouseMask == 0 {
		return
	}
	if ev.Btn&32 != 0 && mode&(vt10x.ModeMouseMotion|vt10x.ModeMouseMany) == 0 {
		return
	}
	cx, cy, ok := innerPos(h, ev.X, ev.Y)
	if !ok {
		return
	}
	cx, cy = cx+1, cy+1 // protocol is 1-based
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
	_, _ = h.p.PTY.Write(b)
}

func (m *Mux) handleWheel(ev *input.Mouse) {
	dir := ev.Btn & 3 // 0 up, 1 down, 2 left, 3 right
	if m.overview {
		switch dir {
		case 0:
			m.moveWorkspaceRel(-1)
		case 1:
			m.moveWorkspaceRel(1)
		case 2:
			m.run(config.Action{Name: "focus_left"})
		case 3:
			m.run(config.Action{Name: "focus_right"})
		}
		return
	}
	h := m.hitAt(ev.X, ev.Y)
	if h == nil {
		return
	}
	mode := paneMode(h.p)
	switch {
	case mode&vt10x.ModeMouseMask != 0:
		m.forwardMouse(h, ev)
	case dir >= 2:
		if dir == 2 {
			m.run(config.Action{Name: "focus_left"})
		} else {
			m.run(config.Action{Name: "focus_right"})
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
		var out []byte
		for i := 0; i < 3; i++ {
			out = append(out, seq...)
		}
		_, _ = h.p.PTY.Write(out)
	}
}

func (m *Mux) handleMouse(ev *input.Mouse) {
	if m.cm != nil {
		return // copy mode is keyboard-only
	}
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
	motion := ev.Btn&32 != 0

	// a drag-selection is in progress: it owns motion and the final release
	if m.selDrag {
		switch {
		case motion && ev.Press:
			m.dragSelection(ev)
			return
		case !ev.Press:
			m.finishSelection()
			return
		}
	}
	if !ev.Press && !motion && m.skipRelease {
		m.skipRelease = false
		return
	}
	if ev.Press && ev.Btn&3 == 0 && !motion {
		m.leftPress(ev)
		return
	}
	// other buttons, releases and drags: pass through to the focused app
	if m.overview {
		return
	}
	if p := m.focusedPane(); p != nil {
		if h := m.hitFor(p); h != nil {
			m.forwardMouse(h, ev)
		}
	}
}

func (m *Mux) leftPress(ev *input.Mouse) {
	if m.barVisible() && ev.Y == m.barY() {
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
	cx, cy, inner := innerPos(h, ev.X, ev.Y)
	shift := ev.Btn&4 != 0
	appMouse := paneMode(h.p)&vt10x.ModeMouseMask != 0

	if appMouse && !shift { // the app handles its own mouse (vim, htop, ...)
		if m.isFocused(h) {
			m.forwardMouse(h, ev)
		} else {
			m.focusHit(h)
			m.skipRelease = true
		}
		return
	}
	if !m.isFocused(h) {
		m.focusHit(h)
	}
	if !inner { // border click: focus only
		m.skipRelease = true
		return
	}
	// start a pane-local selection; the host terminal never sees the drag, so
	// it can't grab neighbouring panes or borders
	m.sel = &Selection{P: h.p, AX: cx, AY: cy, CX: cx, CY: cy, Kind: 'c'}
	m.selDrag = true
}

func (m *Mux) dragSelection(ev *input.Mouse) {
	h := m.hitFor(m.sel.P)
	if h == nil {
		return
	}
	m.sel.CX = max(0, min(h.w-3, ev.X-(h.x+1)))
	m.sel.CY = max(0, min(h.h-3, ev.Y-(h.y+1)))
}

func (m *Mux) finishSelection() {
	m.selDrag = false
	if m.sel == nil {
		return
	}
	if m.sel.AX == m.sel.CX && m.sel.AY == m.sel.CY {
		m.sel = nil // a plain click, nothing selected
		return
	}
	m.copyText(m.extract(m.sel))
}
