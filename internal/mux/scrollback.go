package mux

import (
	"ananta/internal/pane"
	"ananta/internal/vt"
)

// Rows of a pane are addressed in "logical" coordinates: 0 is the oldest
// history line, and the live screen follows the history. In the alternate
// screen (vim, less, ...) there is no history, so logical row 0 is the top of
// the live screen.

type paneView struct {
	sbReal int  // history lines held by the emulator
	hist   int  // history lines visible to the user (0 on the alt screen)
	rows   int  // live screen rows
	cols   int  // live screen columns
	alt    bool // alternate screen active
}

// viewLocked reads the facts; the caller holds p.Term's lock.
func viewLocked(p *pane.Pane) paneView {
	cols, rows := p.Term.Size()
	v := paneView{sbReal: p.Term.ScrollbackLen(), rows: rows, cols: cols}
	v.alt = p.Term.Mode()&vt.ModeAltScreen != 0
	if !v.alt {
		v.hist = v.sbReal
	}
	return v
}

func viewOf(p *pane.Pane) paneView {
	p.Term.Lock()
	defer p.Term.Unlock()
	return viewLocked(p)
}

// total is the number of addressable logical rows.
func (v paneView) total() int { return v.hist + v.rows }

// vtRow converts a logical row to the emulator's row index.
func (v paneView) vtRow(logical int) int {
	if v.alt {
		return v.sbReal + logical
	}
	return logical
}

// viewTop is the logical row shown at the top of the pane.
func viewTop(p *pane.Pane, v paneView) int {
	if !p.Anchored || v.alt {
		return v.hist
	}
	return max(0, min(p.Top, v.hist))
}

func setTop(p *pane.Pane, top int, v paneView) {
	top = max(0, min(top, v.hist))
	if top >= v.hist {
		p.Anchored, p.Top = false, v.hist
		return
	}
	p.Anchored, p.Top = true, top
}

// scrollBack moves the viewport by n lines (n > 0: towards older output).
func (m *Mux) scrollBack(p *pane.Pane, n int) {
	v := viewOf(p)
	if v.alt {
		return
	}
	setTop(p, viewTop(p, v)-n, v)
}

// linesAbove reports how far the viewport is scrolled back (0 = live).
func linesAbove(p *pane.Pane) int {
	if !p.Anchored {
		return 0
	}
	v := viewOf(p)
	return max(0, v.hist-viewTop(p, v))
}
