package mux

import (
	"strings"

	"ananta/internal/vt"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/pane"
)

func appCursor(p *pane.Pane) bool {
	p.Term.Lock()
	defer p.Term.Unlock()
	return p.Term.Mode()&vt.ModeAppCursor != 0
}

// translate converts a key to the bytes the focused app expects (arrow keys
// switch to SS3 form in application-cursor mode).
func (m *Mux) translate(k input.Key) []byte {
	r := k.Raw
	if len(r) == 3 && r[0] == 0x1b && r[1] == '[' && strings.IndexByte("ABCDHF", r[2]) >= 0 {
		if p := m.focusedPane(); p != nil && appCursor(p) {
			return []byte{0x1b, 'O', r[2]}
		}
	}
	return r
}

func (m *Mux) helpKey(k input.Key) {
	if a, ok := m.cfg.Keymap[k.Name]; ok && a.Name == "help" {
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

func (m *Mux) overviewKey(k input.Key) bool {
	switch k.Name {
	case "esc":
		m.overview = false
	case "enter", "space":
		m.overview = false
		m.centerNow(m.curWS())
	case "left", "h":
		m.run(config.Action{Name: "focus_left"})
	case "right", "l":
		m.run(config.Action{Name: "focus_right"})
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

func (m *Mux) handleKeys(ks []input.Key) {
	var pending []byte
	flush := func() {
		if len(pending) > 0 {
			if p := m.focusedPane(); p != nil && !p.Dead {
				p.Anchored = false // typing returns to the live screen
				_, _ = p.Write(pending)
			}
			pending = pending[:0]
		}
	}
	for _, k := range ks {
		if k.OSC != "" { // the terminal answering a colour query: never typed text
			m.handleHostOSC(k.OSC)
			continue
		}
		if k.Mouse != nil {
			flush()
			if m.cfg.Mouse {
				m.handleMouse(k.Mouse)
			}
			continue
		}
		m.sel, m.selDrag = nil, false // any key clears the selection highlight
		if m.cm != nil {
			m.copyKey(k)
			continue
		}
		if m.showHelp {
			m.helpKey(k)
			continue
		}
		if m.prefixPending {
			m.prefixPending = false
			if k.Name == m.cfg.PrefixKey {
				pending = append(pending, k.Raw...)
			} else if a, ok := m.cfg.PrefixMap[k.Name]; ok {
				flush()
				m.run(a)
			}
			continue
		}
		if m.cfg.PrefixKey != "" && k.Name == m.cfg.PrefixKey {
			flush()
			m.prefixPending = true
			continue
		}
		if a, ok := m.cfg.Keymap[k.Name]; ok && k.Name != "" {
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
