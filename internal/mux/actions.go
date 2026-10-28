package mux

import (
	"math"
	"strconv"

	"ananta/internal/config"
)

// run executes one bound action.
func (m *Mux) run(a config.Action) {
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
			m.newColumnFor(ws, p, 0)
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
	case "center_mode":
		if m.scrollMode == "center" {
			m.scrollMode = "fit"
			m.notify("scrolling: fit")
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
		m.newColumnFor(m.ws[n-1], p, 0)
	case "copy_mode":
		m.enterCopyMode()
	case "scroll_up", "scroll_down":
		if p := m.focusedPane(); p != nil {
			n := max(1, p.Rows/2)
			if a.Name == "scroll_down" {
				n = -n
			}
			m.scrollBack(p, n)
		}
	case "detach":
		if m.OnDetach != nil {
			m.OnDetach()
		} else {
			m.notify("not in a session — start one with `ananta`")
		}
	case "toggle_bar":
		m.barHidden = !m.barHidden
	case "help":
		m.showHelp = !m.showHelp
		m.helpScroll = 0
	case "quit":
		m.quit = true
	}
}
