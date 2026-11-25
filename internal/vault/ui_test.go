package vault

import (
	"path/filepath"
	"testing"

	"tst/internal/config"
	"tst/internal/input"
)

func testUI(t *testing.T) *UI {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	u := newUI(cfg, Options{})
	u.sessions = []Session{
		{Name: "alpha", Panes: []Pane{
			{Session: "alpha", ID: 1, Name: "nvim", Cwd: "/home/u/proj", Seq: 1},
			{Session: "alpha", ID: 2, Name: "sh", Cwd: "/home/u", Label: "build", Seq: 2},
		}},
		{Name: "beta"},
	}
	u.buildRows()
	u.sel = 0
	return u
}

func rowText(u *UI) []string {
	var out []string
	for _, r := range u.rows {
		switch r.kind {
		case rowSession:
			out = append(out, "session:"+r.name)
		case rowPane:
			out = append(out, "pane:"+r.name)
		case rowCreate:
			out = append(out, "create")
		}
	}
	return out
}

func TestVaultRowsAndSearch(t *testing.T) {
	u := testUI(t)
	want := []string{"session:alpha", "pane:nvim", "pane:build", "session:beta", "create"}
	got := rowText(u)
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}

	// A custom label replaces the foreground name on the first line.
	if got := u.sessions[0].Panes[1].Display(); got != "build" {
		t.Errorf("Display() = %q, want build", got)
	}

	// "new" finds the create item (as required) and nothing else.
	u.filter = "new"
	u.buildRows()
	if got := rowText(u); len(got) != 1 || got[0] != "create" {
		t.Errorf("filter new = %v, want [create]", got)
	}

	// Matching a pane keeps its session header.
	u.filter = "nvim"
	u.buildRows()
	want = []string{"session:alpha", "pane:nvim"}
	got = rowText(u)
	if len(got) != len(want) {
		t.Fatalf("filter nvim = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filter nvim = %v, want %v", got, want)
		}
	}

	// Matching a session shows all of its panes.
	u.filter = "beta"
	u.buildRows()
	if got := rowText(u); len(got) != 1 || got[0] != "session:beta" {
		t.Errorf("filter beta = %v", got)
	}
}

func TestVaultPendingConfirmation(t *testing.T) {
	u := testUI(t)
	u.sel = 1 // the nvim pane

	u.handleKey(input.Key{Name: "r"})
	if u.pending != 'r' {
		t.Fatalf("pending = %q, want r", u.pending)
	}
	u.handleKey(input.Key{Name: "esc"})
	if u.pending != 0 {
		t.Error("esc should cancel the pending confirmation")
	}
	if u.quit {
		t.Fatal("the first esc must not close the vault")
	}
	u.handleKey(input.Key{Name: "esc"})
	if !u.quit {
		t.Fatal("the second esc should close the vault")
	}

	// Any key other than the repeated one cancels dd too.
	u.quit, u.pending = false, 0
	u.handleKey(input.Key{Name: "d"})
	u.handleKey(input.Key{Name: "j"})
	if u.pending != 0 {
		t.Error("j should cancel the pending delete")
	}
	if u.quit {
		t.Error("cancelling a delete must not close the vault")
	}
}
