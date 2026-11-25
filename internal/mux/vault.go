package mux

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// VaultPane is one marked pane as reported to the vault UI.
type VaultPane struct {
	ID        int    `json:"id"`
	Name      string `json:"name"` // foreground tool / shell
	Cwd       string `json:"cwd"`
	Label     string `json:"label,omitempty"` // custom display name
	Seq       int    `json:"seq"`             // FIFO order inside the session
	Workspace int    `json:"workspace"`
}

// VaultListing is the JSON payload of the "vault_list" control command.
type VaultListing struct {
	Panes []VaultPane `json:"panes"`
}

// markFocused saves the focused pane in the vault.
func (m *Mux) markFocused() {
	p := m.focusedPane()
	if p == nil {
		m.notify("no pane is focused")
		return
	}
	if p.Marked {
		m.notify("pane is already marked")
		return
	}
	m.markSeq++
	p.Marked, p.MarkSeq = true, m.markSeq
	m.notify("pane marked")
}

// unmarkFocused removes the focused pane from the vault.
func (m *Mux) unmarkFocused() {
	p := m.focusedPane()
	if p == nil {
		m.notify("no pane is focused")
		return
	}
	if !p.Marked {
		m.notify("pane is not marked")
		return
	}
	p.Marked, p.Label, p.MarkSeq = false, "", 0
	m.notify("pane removed from the vault")
}

func (m *Mux) markedCount() int {
	n := 0
	for _, ws := range m.ws {
		for _, c := range ws.Cols {
			for _, p := range c.Panes {
				if p.Marked {
					n++
				}
			}
		}
	}
	return n
}

// VaultCommand is the shell command that opens the vault UI in a pane.
func (m *Mux) VaultCommand() string {
	if m.vaultCmd != "" {
		return m.vaultCmd
	}
	return VaultCommandLine("")
}

// VaultCommandLine builds the shell command that runs the vault UI. cfgPath is
// forwarded so the vault reads the same configuration as the caller.
func VaultCommandLine(cfgPath string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "tst"
	}
	cmd := shellQuote(exe) + " vault"
	if cfgPath != "" {
		cmd += " -config " + shellQuote(cfgPath)
	}
	return cmd
}

// openVault is the "vault" action. It always gets a pane of its own; run
// `tst vault` by hand in a pane to open it there instead.
func (m *Mux) openVault() {
	p, err := m.spawn(m.VaultCommand())
	if err != nil {
		m.notify("vault: " + err.Error())
		return
	}
	m.newColumnFor(m.curWS(), p, 0)
	m.notify("vault opened in a new pane")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// FocusPaneID focuses a pane in this session, switching workspace if needed.
// It is used when a client attaches and asks for a specific pane.
func (m *Mux) FocusPaneID(id int) error { return m.focusPaneByID(id) }

// focusPaneByID focuses a pane in this session, switching workspace if needed.
func (m *Mux) focusPaneByID(id int) error {
	for wi, ws := range m.ws {
		for ci, col := range ws.Cols {
			for pi, p := range col.Panes {
				if p.ID != id {
					continue
				}
				m.cur, ws.Focus, col.Focus = wi, ci, pi
				m.overview, m.showHelp = false, false
				m.updateTarget(ws)
				return nil
			}
		}
	}
	return fmt.Errorf("pane %d is no longer running", id)
}

// ctlVaultList reports the marked panes of this session, in FIFO order.
func (m *Mux) ctlVaultList() (string, error) {
	out := VaultListing{Panes: []VaultPane{}}
	for wi, ws := range m.ws {
		for _, col := range ws.Cols {
			for _, p := range col.Panes {
				if !p.Marked {
					continue
				}
				name := p.ForegroundName()
				if name == "" {
					name = p.Name
				}
				out.Panes = append(out.Panes, VaultPane{
					ID: p.ID, Name: name, Cwd: p.Cwd(), Label: p.Label,
					Seq: p.MarkSeq, Workspace: wi + 1,
				})
			}
		}
	}
	sort.SliceStable(out.Panes, func(i, j int) bool { return out.Panes[i].Seq < out.Panes[j].Seq })
	b, err := json.Marshal(out)
	return string(b), err
}

// ctlVaultRename sets a custom display name on a marked pane ("" restores the
// foreground name).
func (m *Mux) ctlVaultRename(id int, label string) error {
	for _, ws := range m.ws {
		for _, col := range ws.Cols {
			for _, p := range col.Panes {
				if p.ID == id {
					if !p.Marked {
						return fmt.Errorf("pane %d is not marked", id)
					}
					p.Label = strings.TrimSpace(label)
					return nil
				}
			}
		}
	}
	return fmt.Errorf("pane %d is no longer running", id)
}

// ctlVaultRemove unmarks a pane without closing it.
func (m *Mux) ctlVaultRemove(id int) error {
	p, err := m.ctlPane(id)
	if err != nil {
		return err
	}
	p.Marked, p.Label, p.MarkSeq = false, "", 0
	return nil
}

// ctlVaultDelete unmarks and closes a pane (the vault's confirmed "dd").
func (m *Mux) ctlVaultDelete(id int) error {
	p, err := m.ctlPane(id)
	if err != nil {
		return err
	}
	p.Marked, p.Label, p.MarkSeq = false, "", 0
	m.removePane(p)
	return nil
}

// ctlVaultJump switches the attached client to another session, or focuses a
// pane in this one.
func (m *Mux) ctlVaultJump(target string, pane int) error {
	if target == "" {
		return fmt.Errorf("no target session")
	}
	if target == m.session {
		if pane != 0 {
			return m.focusPaneByID(pane)
		}
		return nil
	}
	if m.OnSwitch == nil {
		return fmt.Errorf("this session cannot switch to another one")
	}
	return m.OnSwitch(SwitchTarget{Session: target, Pane: pane})
}
