// Package pane is one child process on a PTY plus the terminal emulator that
// tracks what it has drawn.
package pane

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"ananta/internal/vt"
)

type Pane struct {
	ID         int
	Name       string
	Cmd        *exec.Cmd
	PTY        *os.File
	Term       vt.Terminal
	Cols, Rows int
	Dead       bool

	// Viewport into scrollback, managed by the mux. When Anchored, the pane
	// shows history starting at logical row Top instead of following the
	// live screen.
	Anchored bool
	Top      int

	wmu        sync.Mutex // serialises writes to the PTY (keys vs emulator replies)
	noDeadline bool       // PTY fd can't do write deadlines; use the fallback
}

// Options describes the child to start.
type Options struct {
	ID         int
	Shell      string   // "" = $SHELL, then sh
	Cmdline    string   // "" = interactive shell; otherwise run via sh -c
	Dir        string   // working directory ("" = inherit)
	Env        []string // extra environment variables (KEY=value)
	Cols, Rows int
	Scrollback int
}

// New starts the child. dirty is poked when output arrives, exits receives
// the pane when the child's output ends, clip receives OSC 52 clipboard
// requests from the program (may be nil).
func New(o Options, dirty chan<- struct{}, exits chan<- *Pane, clip chan<- string) (*Pane, error) {
	cols, rows := max(o.Cols, 1), max(o.Rows, 1)
	var cmd *exec.Cmd
	var name string
	if o.Cmdline == "" {
		shell := o.Shell
		if shell == "" {
			shell = os.Getenv("SHELL")
		}
		if shell == "" {
			if p, err := exec.LookPath("sh"); err == nil {
				shell = p
			} else {
				shell = "/bin/sh"
			}
		}
		cmd = exec.Command(shell)
		name = filepath.Base(shell)
	} else {
		sh, err := exec.LookPath("sh")
		if err != nil {
			sh = "/bin/sh"
		}
		cmd = exec.Command(sh, "-c", o.Cmdline)
		name = strings.Fields(o.Cmdline)[0]
	}
	if o.Dir != "" {
		if st, err := os.Stat(o.Dir); err == nil && st.IsDir() {
			cmd.Dir = o.Dir
		}
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "ANANTA=1")
	cmd.Env = append(cmd.Env, o.Env...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	p := &Pane{ID: o.ID, Name: name, Cmd: cmd, PTY: ptmx, Cols: cols, Rows: rows}
	// the emulator answers terminal queries (cursor position, device
	// attributes) by writing to the PTY; route those through the bounded writer
	p.Term = vt.New(vt.WithSize(cols, rows), vt.WithWriter(replyWriter{p}))
	p.Term.SetScrollbackLimit(o.Scrollback)
	if clip != nil {
		p.Term.SetClipboardHandler(func(text string) {
			select { // runs under the emulator lock: never block
			case clip <- text:
			default:
			}
		})
	}

	go func() {
		buf := make([]byte, 32*1024)
		var carry []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				data := append(carry, buf[:n]...)
				w, _ := p.Term.Write(data)
				carry = append([]byte(nil), data[w:]...)
				select {
				case dirty <- struct{}{}:
				default:
				}
			}
			if err != nil {
				exits <- p
				_ = cmd.Wait()
				return
			}
		}
	}()
	return p, nil
}

func (p *Pane) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	if p.Dead || (cols == p.Cols && rows == p.Rows) {
		return
	}
	p.Cols, p.Rows = cols, rows
	_ = pty.Setsize(p.PTY, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	p.Term.Resize(cols, rows)
}

func (p *Pane) Close() {
	if p.Dead {
		return
	}
	p.Dead = true
	_ = p.PTY.Close()
	if p.Cmd.Process != nil {
		_ = p.Cmd.Process.Signal(syscall.SIGHUP)
	}
}

// Cwd returns the child's current working directory (Linux: via /proc), or ""
// when unknown.
func (p *Pane) Cwd() string {
	if p.Cmd == nil || p.Cmd.Process == nil {
		return ""
	}
	d, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", p.Cmd.Process.Pid))
	if err != nil {
		return ""
	}
	return d
}

// writeBounded writes b to the child but gives up after limit, so a child
// that has stopped reading its input can't freeze the multiplexer. The
// deadline is cleared again afterwards: an expired deadline left on the fd
// would make every later write fail (including the emulator's replies).
func (p *Pane) writeBounded(b []byte, limit time.Duration) (int, error) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if !p.noDeadline {
		if err := p.PTY.SetWriteDeadline(time.Now().Add(limit)); err == nil {
			n, err := p.PTY.Write(b)
			_ = p.PTY.SetWriteDeadline(time.Time{})
			return n, err
		}
		p.noDeadline = true // not pollable on this platform
	}
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := p.PTY.Write(b)
		ch <- result{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-time.After(limit):
		return 0, os.ErrDeadlineExceeded
	}
}

// Write sends keyboard / paste input to the child.
func (p *Pane) Write(b []byte) (int, error) {
	if p.Dead {
		return 0, os.ErrClosed
	}
	return p.writeBounded(b, 250*time.Millisecond)
}

// replyWriter carries the emulator's answers to terminal queries.
type replyWriter struct{ p *Pane }

func (w replyWriter) Write(b []byte) (int, error) {
	if w.p.Dead {
		return len(b), nil
	}
	return w.p.writeBounded(b, 100*time.Millisecond)
}
