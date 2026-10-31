package mux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"ananta/internal/config"
)

// barClick handles a click at column x of the status bar: a workspace tab
// switches workspace, a clickable widget runs its action / command.
func (m *Mux) barClick(x int, right bool) {
	for _, t := range m.tabs {
		if x < t.x0 || x >= t.x1 {
			continue
		}
		switch {
		case t.widget != "":
			m.clickWidget(t.widget, right)
		case !right:
			m.cur = t.ws
		}
		return
	}
}

func (m *Mux) clickWidget(name string, right bool) {
	w, ok := m.cfg.Bar.Widgets[name]
	if !ok {
		return
	}
	cmd, act := w.OnClick, w.OnClickAction
	if right {
		cmd, act = w.OnRightClick, w.OnRightClickAction
	}
	if act = strings.TrimSpace(act); act != "" {
		name, arg, _ := strings.Cut(act, " ")
		m.run(config.Action{Name: name, Arg: strings.TrimSpace(arg)})
	}
	if strings.TrimSpace(cmd) != "" {
		m.runShell(cmd)
	}
}

// tailBuffer keeps the first bytes written to it (a command's error output).
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	if room := 400 - len(t.b); room > 0 {
		t.b = append(t.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// runShell runs a command line in the background. Its environment names the
// session and focused pane, so `ananta open ...` inside it acts on this
// session. A failure is shown in the status bar.
func (m *Mux) runShell(cmdline string) {
	cmd := exec.Command("sh", "-c", cmdline)
	cmd.Env = os.Environ()
	if m.session != "" {
		cmd.Env = append(cmd.Env, "ANANTA_SESSION="+m.session)
	}
	if p := m.focusedPane(); p != nil {
		cmd.Env = append(cmd.Env, fmt.Sprintf("ANANTA_PANE=%d", p.ID))
	}
	if dir, err := os.UserHomeDir(); err == nil {
		cmd.Dir = dir
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		m.notify("click command: " + err.Error())
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			msg := strings.TrimSpace(strings.SplitN(string(stderr.b), "\n", 2)[0])
			if msg == "" {
				msg = err.Error()
			}
			select {
			case m.notifyCh <- "click command failed: " + msg:
			default:
			}
		}
	}()
}
