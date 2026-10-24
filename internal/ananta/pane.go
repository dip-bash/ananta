package ananta

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

type Pane struct {
	ID         int
	Name       string
	Cmd        *exec.Cmd
	PTY        *os.File
	Term       vt10x.Terminal
	Cols, Rows int
	Dead       bool
}

func newPane(id int, shell, cmdline string, cols, rows int, dirty chan<- struct{}, exits chan<- *Pane) (*Pane, error) {
	cols, rows = max(cols, 1), max(rows, 1)
	var cmd *exec.Cmd
	var name string
	if cmdline == "" {
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
		cmd = exec.Command(sh, "-c", cmdline)
		name = strings.Fields(cmdline)[0]
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "ANANTA=1", "SCROLLMUX=1")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	p := &Pane{ID: id, Name: name, Cmd: cmd, PTY: ptmx, Cols: cols, Rows: rows}
	p.Term = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptmx))

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
