package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"ananta/internal/config"
	"ananta/internal/mux"
	"ananta/internal/vt"
)

// screenText replays everything the server has drawn into a terminal emulator
// and returns the resulting screen. The renderer sends only changed cells (and
// a pane that is still animating open reveals text piece by piece), so output
// must be judged by the screen it produces, never by searching raw bytes.
func screenText(out []byte, cols, rows int) string {
	term := vt.New(vt.WithSize(cols, rows))
	_, _ = term.Write(out)
	term.Lock()
	defer term.Unlock()
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			c := term.Cell(x, y).Char
			if c == 0 {
				c = ' '
			}
			sb.WriteRune(c)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// readUntil reads frames until the screen contains want (when want != ""), or
// until a frame of type stopOn arrives. It returns the last frame type seen.
func readUntil(t *testing.T, conn net.Conn, want string, stopOn byte) (found bool, last byte) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var acc []byte
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		typ, payload, err := readFrame(conn)
		if err != nil {
			continue
		}
		last = typ
		if typ == MsgOutput {
			acc = append(acc, payload...)
			if want != "" && strings.Contains(screenText(acc, 80, 24), want) {
				return true, typ
			}
		}
		if stopOn != 0 && typ == stopOn {
			return false, typ
		}
	}
	if want != "" {
		t.Logf("screen at timeout:\n%s", screenText(acc, 80, 24))
	}
	return false, last
}

func attach(t *testing.T, name string) net.Conn {
	t.Helper()
	conn, err := dial(name)
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := json.Marshal(Size{Cols: 80, Rows: 24})
	if err := writeFrame(conn, MsgHello, hello); err != nil {
		t.Fatal(err)
	}
	return conn
}

func skipWithoutPTY(t *testing.T) {
	t.Helper()
	m, s, err := pty.Open()
	if err != nil {
		t.Skip("no PTY available in this environment: ", err)
	}
	m.Close()
	s.Close()
}

func TestDetachKeepsPanesAlive(t *testing.T) {
	skipWithoutPTY(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", shellForTest())
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.WallpaperSpec = nil // keep the test about sessions, not the background program
	done := make(chan error, 1)
	go func() { done <- Serve(cfg, "t") }()
	for i := 0; i < 100 && !Exists("t"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !Exists("t") {
		t.Fatal("server did not start")
	}

	c1 := attach(t, "t")
	if typ, _, err := readFrame(c1); err != nil || typ != MsgSetup {
		t.Fatalf("expected setup frame, got %c %v", typ, err)
	}
	_ = writeFrame(c1, MsgInput, []byte("\x1b\r")) // new_pane: a session starts empty
	time.Sleep(100 * time.Millisecond)
	_ = writeFrame(c1, MsgInput, []byte("echo hello-$((40+2))\r"))
	if ok, _ := readUntil(t, c1, "hello-42", 0); !ok {
		t.Fatal("never saw command output")
	}
	_ = writeFrame(c1, MsgInput, []byte("\x1bd")) // detach chord (alt+d)
	if ok, last := readUntil(t, c1, "", MsgDetached); ok || last != MsgDetached {
		t.Fatalf("expected detach notice, last frame %c", last)
	}
	c1.Close()

	time.Sleep(200 * time.Millisecond)
	infos, err := List()
	if err != nil || len(infos) != 1 || infos[0].Attached || infos[0].Panes != 1 {
		t.Fatalf("after detach: %+v err=%v", infos, err)
	}

	c2 := attach(t, "t") // the earlier output must be repainted from server state
	if typ, _, _ := readFrame(c2); typ != MsgSetup {
		t.Fatal("no setup on reattach")
	}
	if ok, _ := readUntil(t, c2, "hello-42", 0); !ok {
		t.Fatal("state lost across detach")
	}
	c2.Close()

	if err := Kill("t"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after kill")
	}
}

func TestFrameRoundTripAndNames(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, MsgInput, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	typ, p, err := readFrame(&buf)
	if err != nil || typ != MsgInput || string(p) != "abc" {
		t.Fatalf("round trip: %c %q %v", typ, p, err)
	}
	for name, ok := range map[string]bool{"main": true, "work-1.a": true, "": false, "a/b": false, "../x": false} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) != %v", name, ok)
		}
	}
}

// A kill must not be lost when the work queue is momentarily full.
func TestStopSurvivesFullQueue(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	s := &Server{
		post: make(chan func(), 2),
		stop: make(chan struct{}),
		m:    mux.New(cfg, bufio.NewWriter(io.Discard)),
	}
	s.post <- func() {}
	s.post <- func() {} // queue is now full
	s.requestStop()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case fn := <-s.post:
			fn()
			if s.m.PaneCount() == 0 && s.m.Quitting() {
				return
			}
		case <-deadline:
			t.Fatal("quit was dropped")
		}
	}
}

// shellForTest lets the test run against any shell: TEST_SHELL=/bin/bash go test ...
func shellForTest() string {
	if s := os.Getenv("TEST_SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func TestControlCommands(t *testing.T) {
	skipWithoutPTY(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", shellForTest())
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.WallpaperSpec = nil
	cfg.Animate = false
	done := make(chan error, 1)
	go func() { done <- Serve(cfg, "ctl") }()
	for i := 0; i < 100 && !Exists("ctl"); i++ {
		time.Sleep(50 * time.Millisecond)
	}

	// before anyone has attached the session has no screen size yet
	if _, err := Control("ctl", mux.Request{Op: "list"}); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected a not-ready error, got %v", err)
	}
	c := attach(t, "ctl") // a client gives the session its size
	defer c.Close()
	for i := 0; i < 100; i++ { // wait until the loop is running
		if _, err := Control("ctl", mux.Request{Op: "list"}); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	run := func(r mux.Request) string {
		t.Helper()
		out, err := Control("ctl", r)
		if err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
		return out
	}
	// open a program in workspace 3 at 25% width
	id := run(mux.Request{Op: "open", Command: "sleep 30", Workspace: 3, Width: 0.25})
	if id == "" {
		t.Fatal("open returned no pane id")
	}
	list := run(mux.Request{Op: "list"})
	var line string
	for _, l := range strings.Split(list, "\n") {
		if strings.HasPrefix(l, id+" ") {
			line = l
		}
	}
	f := strings.Fields(line)
	if len(f) < 5 || f[1] != "3" || f[3] != "25%" || f[4] != "sleep" {
		t.Fatalf("pane %s should be in workspace 3 at 25%%: %q\n%s", id, line, list)
	}
	if strings.Contains(line, "*") {
		t.Fatal("opening in another workspace must not steal the focus")
	}

	// resize it, stack another pane under it, send text, then close
	run(mux.Request{Op: "width", Pane: atoi(t, id), Width: 0.5})
	if !strings.Contains(run(mux.Request{Op: "list"}), "50%") {
		t.Fatal("width command had no effect")
	}
	run(mux.Request{Op: "workspace", Workspace: 3})
	id2 := run(mux.Request{Op: "open", Command: "cat", Workspace: 3, Below: true})
	run(mux.Request{Op: "send", Pane: atoi(t, id2), Text: "hi", Enter: true})
	run(mux.Request{Op: "action", Action: "overview"})
	run(mux.Request{Op: "action", Action: "overview"})

	// --keep-focus: a pane opened with it must not take the focus
	before := focusedID(run(mux.Request{Op: "list"}))
	idK := run(mux.Request{Op: "open", Command: "sleep 30", KeepFocus: true})
	if got := focusedID(run(mux.Request{Op: "list"})); got != before || got == idK {
		t.Fatalf("keep-focus: focus moved from pane %q to %q", before, got)
	}
	idF := run(mux.Request{Op: "open", Command: "sleep 30"})
	if got := focusedID(run(mux.Request{Op: "list"})); got != idF {
		t.Fatalf("without keep-focus the new pane %q should be focused, got %q", idF, got)
	}
	run(mux.Request{Op: "close", Pane: atoi(t, id2)})
	if strings.Contains(run(mux.Request{Op: "list"}), "cat") {
		t.Fatal("closed pane is still listed")
	}

	// errors
	for name, r := range map[string]mux.Request{
		"bad workspace": {Op: "open", Command: "true", Workspace: 12},
		"unknown pane":  {Op: "close", Pane: 9999},
		"bad action":    {Op: "action", Action: "no_such_action"},
		"bad direction": {Op: "focus", Direction: "sideways"},
		"bad op":        {Op: "explode"},
	} {
		if _, err := Control("ctl", r); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Control("no-such-session", mux.Request{Op: "list"}); err == nil || !strings.Contains(err.Error(), "no running session") {
		t.Errorf("missing session: %v", err)
	}

	if err := Kill("ctl"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// focusedID returns the id of the pane marked with * in `list` output.
func focusedID(list string) string {
	for _, l := range strings.Split(list, "\n") {
		if strings.HasSuffix(l, " *") {
			return strings.Fields(l)[0]
		}
	}
	return ""
}
