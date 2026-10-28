package mux

import (
	"ananta/internal/pane"
	"fmt"
	"strconv"
	"strings"

	"ananta/internal/config"
)

// Request is a command sent to a running session from outside (the
// `ananta open|workspace|focus|width|close|send|list|action` commands). It is
// executed on the mux goroutine by Control.
type Request struct {
	Op        string  `json:"op"` // open workspace focus width close send list action
	Command   string  `json:"command,omitempty"`
	Dir       string  `json:"dir,omitempty"`
	Workspace int     `json:"workspace,omitempty"`  // 1..9; 0 = current
	Width     float64 `json:"width,omitempty"`      // fraction of the screen width; 0 = default
	Below     bool    `json:"below,omitempty"`      // open: stack under the focused pane
	Focus     bool    `json:"focus,omitempty"`      // open: also switch to that workspace
	KeepFocus bool    `json:"keep_focus,omitempty"` // open: leave keyboard focus where it was
	Pane      int     `json:"pane,omitempty"`       // pane id; 0 = the focused pane
	Text      string  `json:"text,omitempty"`       // send
	Enter     bool    `json:"enter,omitempty"`      // send: press Enter afterwards
	Direction string  `json:"direction,omitempty"`  // focus: left right up down
	Action    string  `json:"action,omitempty"`     // action: any bound action name
	Arg       string  `json:"arg,omitempty"`
}

// ParseWidth reads a column width: "0.33" or "33%" or "33" (a bare number
// above 1 is a percentage). The result is a fraction of the screen width.
func ParseWidth(s string) (float64, error) {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("bad width %q (use 0.33, 33%% or 33)", s)
	}
	if pct || v > 1 {
		v /= 100
	}
	if v <= 0 || v > 1 {
		return 0, fmt.Errorf("width %q out of range: must be between 1%% and 100%%", s)
	}
	return v, nil
}

// Control runs one request and returns its output (printed by the CLI).
func (m *Mux) Control(r Request) (string, error) {
	if m.W < 1 {
		return "", fmt.Errorf("session has no screen size yet: attach to it once")
	}
	switch r.Op {
	case "open":
		return m.ctlOpen(r)
	case "workspace":
		n, err := m.ctlWorkspace(r.Workspace)
		if err == nil {
			m.cur = n
		}
		return "", err
	case "focus":
		return "", m.ctlFocus(r)
	case "width":
		return "", m.ctlWidth(r)
	case "close":
		p, err := m.ctlPane(r.Pane)
		if err != nil {
			return "", err
		}
		m.removePane(p)
		return "", nil
	case "send":
		p, err := m.ctlPane(r.Pane)
		if err != nil {
			return "", err
		}
		text := r.Text
		if r.Enter {
			text += "\r"
		}
		_, werr := p.Write([]byte(text))
		return "", werr
	case "list":
		return m.ctlList(), nil
	case "action":
		if config.ActionIndex(r.Action) == len(config.ActionInfo) {
			return "", fmt.Errorf("unknown action %q", r.Action)
		}
		m.run(config.Action{Name: r.Action, Arg: r.Arg})
		return "", nil
	}
	return "", fmt.Errorf("unknown command %q", r.Op)
}

// ctlWorkspace validates a 1-based workspace number (0 = current) and
// returns its index.
func (m *Mux) ctlWorkspace(n int) (int, error) {
	if n == 0 {
		return m.cur, nil
	}
	if n < 1 || n > len(m.ws) {
		return 0, fmt.Errorf("workspace must be 1..%d", len(m.ws))
	}
	return n - 1, nil
}

// ctlPane finds the target pane: by id, or the focused one when id is 0.
func (m *Mux) ctlPane(id int) (*pane.Pane, error) {
	if id == 0 {
		p := m.focusedPane()
		if p == nil {
			return nil, fmt.Errorf("no pane is focused")
		}
		return p, nil
	}
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				if p.ID == id {
					return p, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no pane with id %d (see `ananta list`)", id)
}

func (m *Mux) ctlOpen(r Request) (string, error) {
	wi, err := m.ctlWorkspace(r.Workspace)
	if err != nil {
		return "", err
	}
	ws := m.ws[wi]
	frac := r.Width
	if frac <= 0 {
		frac = m.cfg.DefaultColumnWidth
	}
	if frac > 1 {
		return "", fmt.Errorf("width must be between 1%% and 100%%")
	}
	dir := r.Dir
	if dir == "" {
		dir = m.startDir
	}
	p, err := m.spawnWith(r.Command, dir, frac)
	if err != nil {
		return "", err
	}
	prevCol, prevPane := ws.Focus, 0
	if len(ws.Cols) > 0 {
		prevPane = ws.Cols[ws.Focus].Focus
	}
	if r.Below && len(ws.Cols) > 0 {
		m.stackBelow(ws, p)
	} else {
		m.newColumnFor(ws, p, frac)
	}
	if r.KeepFocus && len(ws.Cols) > 1 || r.KeepFocus && r.Below {
		// the new column goes right of the focused one, so the old index is still valid
		ws.Focus = prevCol
		ws.Cols[prevCol].Focus = prevPane
	}
	if r.Focus {
		m.cur = wi
	}
	return strconv.Itoa(p.ID), nil
}

func (m *Mux) ctlFocus(r Request) error {
	switch r.Direction {
	case "left", "right", "up", "down":
		m.run(config.Action{Name: "focus_" + r.Direction})
		return nil
	}
	return fmt.Errorf("direction must be left, right, up or down")
}

func (m *Mux) ctlWidth(r Request) error {
	if r.Width <= 0 || r.Width > 1 {
		return fmt.Errorf("width must be between 1%% and 100%%")
	}
	p, err := m.ctlPane(r.Pane)
	if err != nil {
		return err
	}
	ws, _, ci, _, ok := m.findPane(p)
	if !ok {
		return fmt.Errorf("pane not found")
	}
	ws.Cols[ci].WFrac = r.Width
	return nil
}

func (m *Mux) ctlList() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-4s %-3s %-4s %-6s %s\n", "ID", "WS", "COL", "WIDTH", "PANE")
	for wi, ws := range m.ws {
		for ci, col := range ws.Cols {
			for pi, p := range col.Panes {
				p.Term.Lock()
				title := p.Term.Title()
				p.Term.Unlock()
				mark := ""
				if wi == m.cur && ci == ws.Focus && pi == col.Focus {
					mark = " *"
				}
				if title != "" && title != p.Name {
					title = p.Name + " — " + title
				} else {
					title = p.Name
				}
				fmt.Fprintf(&sb, "%-4d %-3d %-4d %-6s %s%s\n", p.ID, wi+1, ci+1,
					fmt.Sprintf("%.0f%%", col.WFrac*100), title, mark)
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}
