package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"tst/internal/mux"

	"golang.org/x/term"
)

func dial(name string) (net.Conn, error) {
	return net.DialTimeout("unix", SockPath(name), 2*time.Second)
}

// Exists reports whether a live session with this name is running.
func Exists(name string) bool {
	c, err := dial(name)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// EnsureServer starts a background server for the session if none is running.
// cfgPath is passed through so the server reads the same config.
func EnsureServer(name, cfgPath string) error {
	if Exists(name) {
		return nil
	}
	// a stale socket is removed by the server itself, under its lock
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var args []string
	if cfgPath != "" {
		args = append(args, "-config", cfgPath)
	}
	args = append(args, "server", name)
	cmd := exec.Command(exe, args...)
	logf, err := os.OpenFile(LogPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		cmd.Stdout, cmd.Stderr = logf, logf
		defer logf.Close()
	}
	cmd.Dir = "/"                                        // don't pin the directory tst was launched from (it may be unmounted or deleted)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive the terminal going away
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if Exists(name) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("session server did not start (see %s)", LogPath(name))
}

// Attach connects this terminal to the session and relays until detached.
func Attach(name string) error { return AttachPane(name, 0) }

// AttachPane is Attach, then focuses a specific pane (the vault's jump).
func AttachPane(name string, pane int) error {
	conn, err := dial(name)
	if err != nil {
		return fmt.Errorf("no session %q (start one with `tst new %s`)", name, name)
	}
	defer conn.Close()
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("tst must run inside a terminal")
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	hello, _ := json.Marshal(Size{Cols: w, Rows: h, Cwd: cwd, Pane: pane})
	if err := writeFrame(conn, MsgHello, hello); err != nil {
		return err
	}
	typ, payload, err := readFrame(conn)
	if err != nil || typ != MsgSetup {
		return errors.New("session did not accept the connection")
	}
	setup, teardown := splitSetup(payload)

	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	os.Stdout.WriteString(setup)
	var wmu sync.Mutex
	send := func(t byte, p []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = writeFrame(conn, t, p)
	}

	go func() { // keyboard + mouse -> server
		buf := make([]byte, 8192)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				send(MsgInput, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
				b, _ := json.Marshal(Size{Cols: w, Rows: h})
				send(MsgResize, b)
			}
		}
	}()

	msg := "connection lost"
	var next *Switch
loop:
	for {
		typ, payload, err := readFrame(conn)
		if err != nil {
			break
		}
		switch typ {
		case MsgOutput:
			os.Stdout.Write(payload)
		case MsgDetached:
			msg = string(payload)
			break loop
		case MsgEnded:
			msg = "session ended"
			break loop
		case MsgSwitch:
			var sw Switch
			if json.Unmarshal(payload, &sw) == nil && sw.Session != "" {
				next = &sw
			}
			break loop
		}
	}
	signal.Stop(winch)
	os.Stdout.WriteString(teardown)
	_ = term.Restore(fd, old)
	if next != nil {
		// Replace the process: its stdin reader would otherwise keep reading
		// the terminal and steal keys from the new session.
		_ = conn.Close()
		return AttachExec(next.Session, next.Pane, next.Config)
	}
	fmt.Fprintf(os.Stderr, "[%s: %s]\n", name, msg)
	return nil
}

// AttachExec replaces the current process with a fresh attach client. It is
// used after a session switch so no goroutine from the old connection survives
// to read the terminal.
func AttachExec(name string, pane int, cfgPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return AttachPane(name, pane)
	}
	args := []string{exe}
	if cfgPath != "" {
		args = append(args, "-config", cfgPath)
	}
	args = append(args, "attach", name)
	if pane != 0 {
		args = append(args, strconv.Itoa(pane))
	}
	return syscall.Exec(exe, args, os.Environ())
}

// Kill ends a session (and every program running in it).
func Kill(name string) error {
	conn, err := dial(name)
	if err != nil {
		return fmt.Errorf("no session %q", name)
	}
	defer conn.Close()
	if err := writeFrame(conn, MsgKill, nil); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, _ = readFrame(conn) // returns when the server closes the socket
	return nil
}

// List returns the live sessions, removing stale sockets it finds.
func List() ([]Info, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".sock")
		if !ok {
			continue
		}
		conn, err := dial(name)
		if err != nil {
			_ = os.Remove(SockPath(name))
			continue
		}
		_ = writeFrame(conn, MsgInfo, nil)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		typ, payload, err := readFrame(conn)
		conn.Close()
		if err != nil || typ != MsgInfoResp {
			continue
		}
		var in Info
		if json.Unmarshal(payload, &in) == nil {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Started != out[j].Started {
			return out[i].Started < out[j].Started // creation order, as Info documents
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Control sends one command to a running session and returns its output.
func Control(name string, req mux.Request) (string, error) {
	conn, err := dial(name)
	if err != nil {
		return "", fmt.Errorf("no running session %q (start one with `tst new %s`)", name, name)
	}
	defer conn.Close()
	payload, _ := json.Marshal(req)
	if err := writeFrame(conn, MsgCommand, payload); err != nil {
		return "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	typ, p, err := readFrame(conn)
	if err != nil || typ != MsgCommandResp {
		return "", fmt.Errorf("no answer from session %q", name)
	}
	var resp Response
	if err := json.Unmarshal(p, &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Output, nil
}
