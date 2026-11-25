package mux

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"tst/internal/config"
	"tst/internal/pane"
)

func vaultMux(t *testing.T) *Mux {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.WallpaperSpec = nil
	cfg.Animate = false
	m := New(cfg, bufio.NewWriter(io.Discard))
	m.SetSize(100, 30)
	m.SetSessionName("sess")
	t.Cleanup(m.Shutdown)
	return m
}

func addPane(t *testing.T, m *Mux, cmdline string) *pane.Pane {
	t.Helper()
	p, err := m.spawnWith(cmdline, t.TempDir(), 0.5)
	if err != nil {
		t.Fatal(err)
	}
	m.newColumnFor(m.curWS(), p, 0.5)
	return p
}

func listPanes(t *testing.T, m *Mux) []VaultPane {
	t.Helper()
	raw, err := m.ctlVaultList()
	if err != nil {
		t.Fatal(err)
	}
	var l VaultListing
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatal(err)
	}
	return l.Panes
}

func TestVaultMarkOrderAndProtection(t *testing.T) {
	m := vaultMux(t)
	p1 := addPane(t, m, "sleep 30")
	p2 := addPane(t, m, "tail -f /dev/null")
	time.Sleep(200 * time.Millisecond)

	// Mark the second pane first, then the first: the vault keeps FIFO order.
	m.markFocused()
	if err := m.ctlVaultJump("sess", p1.ID); err != nil {
		t.Fatal(err)
	}
	m.markFocused()

	panes := listPanes(t, m)
	if len(panes) != 2 {
		t.Fatalf("marked panes = %d, want 2", len(panes))
	}
	if panes[0].ID != p2.ID || panes[1].ID != p1.ID {
		t.Fatalf("vault order = [%d %d], want [%d %d]", panes[0].ID, panes[1].ID, p2.ID, p1.ID)
	}
	if panes[1].Name != "sleep" {
		t.Errorf("foreground name = %q, want sleep", panes[1].Name)
	}
	if panes[1].Cwd == "" {
		t.Error("cwd should be reported")
	}

	// Every close path is refused while a pane is marked.
	if _, err := m.Control(Request{Op: "close", Pane: p1.ID}); err == nil || err.Error() != "pane is marked" {
		t.Fatalf("close marked pane: %v, want 'pane is marked'", err)
	}
	m.run(config.Action{Name: "quit"})
	if m.quit {
		t.Fatal("quit must be refused while a pane is marked")
	}

	// Rename and remove keep the pane alive.
	if err := m.ctlVaultRename(p1.ID, " build "); err != nil {
		t.Fatal(err)
	}
	if got := listPanes(t, m)[1].Label; got != "build" {
		t.Errorf("label = %q, want build", got)
	}
	if err := m.ctlVaultRemove(p1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Control(Request{Op: "close", Pane: p1.ID}); err != nil {
		t.Fatalf("close after unmark: %v", err)
	}

	// dd (vault_delete) closes a marked pane and drops its mark.
	if err := m.ctlVaultDelete(p2.ID); err != nil {
		t.Fatal(err)
	}
	if got := listPanes(t, m); len(got) != 0 {
		t.Fatalf("vault still lists %d panes", len(got))
	}
	if m.totalPanes() != 0 {
		t.Fatalf("panes left = %d, want 0", m.totalPanes())
	}
}

func TestVaultMarkedBorderColor(t *testing.T) {
	m := vaultMux(t)
	marked := addPane(t, m, "sleep 30")
	addPane(t, m, "sleep 30")
	marked.Marked = true // focus stays on the second pane

	m.render()
	var hit *hit
	for i := range m.hits {
		if m.hits[i].p == marked {
			hit = &m.hits[i]
		}
	}
	if hit == nil {
		t.Fatal("marked pane was not drawn")
	}
	want := config.ParseColor(m.cfg.Theme.MarkedBorder)
	scr := m.rend.Cur()
	if got := scr.C[hit.y*scr.W+hit.x].FG; got != want {
		t.Errorf("marked border color = %v, want %v", got, want)
	}
	if got := scr.C[hit.y*scr.W+hit.x].FG; got == config.ParseColor(m.cfg.Theme.FocusBorder) {
		t.Error("marked border must differ from the focused border")
	}

	// Focus wins when the pane is both focused and marked.
	if err := m.ctlVaultJump("sess", marked.ID); err != nil {
		t.Fatal(err)
	}
	m.render()
	for i := range m.hits {
		if m.hits[i].p == marked {
			hit = &m.hits[i]
		}
	}
	scr = m.rend.Cur()
	if got := scr.C[hit.y*scr.W+hit.x].FG; got != config.ParseColor(m.cfg.Theme.FocusBorder) {
		t.Errorf("focused marked border = %v, want the focus color", got)
	}
}
