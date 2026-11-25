// Package vault implements the saved-pane registry view and its terminal UI.
//
// Marks live on the panes themselves (inside their session server), which is
// what makes the registry shared by every tstrip session without writing
// anything to disk. Load() is the single place that joins those live sessions
// into the list the vault shows.
package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"tst/internal/mux"
	"tst/internal/session"
)

// Pane is one marked pane.
type Pane struct {
	Session   string
	ID        int
	Name      string // foreground tool or shell
	Cwd       string
	Label     string // custom display name ("" = Name)
	Seq       int
	Workspace int
}

// Display is the first line shown for the pane.
func (p Pane) Display() string {
	if p.Label != "" {
		return p.Label
	}
	if p.Name != "" {
		return p.Name
	}
	return "pane " + strconv.Itoa(p.ID)
}

// Session is one live tstrip session and its marked panes.
type Session struct {
	Name    string
	Started int64
	Panes   []Pane
}

// Load returns every live session (including sessions with no marks), in
// creation order, with each session's marked panes in FIFO order.
func Load() ([]Session, error) {
	infos, err := session.List()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(infos, func(i, j int) bool {
		a, b := infos[i], infos[j]
		if a.Started != b.Started {
			if a.Started == 0 {
				return false
			}
			if b.Started == 0 {
				return true
			}
			return a.Started < b.Started
		}
		return a.Name < b.Name
	})
	out := make([]Session, len(infos))
	var wg sync.WaitGroup
	for i, in := range infos {
		wg.Add(1)
		go func(i int, name string, started int64, panes int) {
			defer wg.Done()
			s := Session{Name: name, Started: started}
			// A session with no panes has no marks either, and an idle session
			// that has not started its mux loop cannot answer a control request.
			// Skip the round trip in both cases.
			if panes == 0 {
				out[i] = s
				return
			}
			raw, err := session.Control(name, mux.Request{Op: "vault_list"})
			if err == nil {
				var listing mux.VaultListing
				if json.Unmarshal([]byte(raw), &listing) == nil {
					for _, p := range listing.Panes {
						s.Panes = append(s.Panes, Pane{
							Session: name, ID: p.ID, Name: p.Name, Cwd: p.Cwd,
							Label: p.Label, Seq: p.Seq, Workspace: p.Workspace,
						})
					}
				}
			}
			sort.SliceStable(s.Panes, func(i, j int) bool { return s.Panes[i].Seq < s.Panes[j].Seq })
			out[i] = s
		}(i, in.Name, in.Started, in.Panes)
	}
	wg.Wait()
	return out, nil
}

// shortCwd replaces the home directory with "~" and keeps the rest readable.
func shortCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if cwd == home {
			return "~"
		}
		if strings.HasPrefix(cwd, home+string(filepath.Separator)) {
			return "~" + strings.TrimPrefix(cwd, home)
		}
	}
	return cwd
}
