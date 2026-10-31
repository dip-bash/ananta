package mux

import (
	"ananta/internal/input"
	"ananta/internal/pane"
)

// Programs ask "what colour is the background?" to choose between a light and
// a dark theme. We don't know, but the user's terminal does: we ask it when a
// client attaches and relay the answer to every pane.

// QueryHostColors asks the attached terminal for its foreground and
// background colour; the replies arrive as input (see handleHostOSC).
func (m *Mux) QueryHostColors() {
	if m.attached {
		m.rend.Raw("\x1b]10;?\x1b\\\x1b]11;?\x1b\\")
	}
}

func (m *Mux) handleHostOSC(payload string) {
	num, rgb, ok := input.ParseOSCColor(payload)
	if !ok {
		return
	}
	switch num {
	case 10:
		m.hostFG = rgb
	case 11:
		m.hostBG = rgb
	default:
		return
	}
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				m.applyHostColors(p)
			}
		}
	}
	if m.wall != nil {
		m.applyHostColors(m.wall)
	}
}

func (m *Mux) applyHostColors(p *pane.Pane) {
	p.Term.Lock()
	p.Term.SetDefaultColors(m.hostFG, m.hostBG)
	p.Term.Unlock()
}
