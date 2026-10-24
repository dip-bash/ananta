// Package app owns the process lifecycle: raw terminal mode, alternate
// screen, mouse / keyboard protocol switches, signal handling, input reader.
package app

import (
	"bufio"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/mux"
)

func readInput(keys chan<- []input.Key) {
	buf := make([]byte, 8192)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			keys <- input.ParseKeys(data)
		}
		if err != nil {
			close(keys)
			return
		}
	}
}

// Run starts the multiplexer and blocks until it exits.
func Run(cfg *config.Config) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("ananta must run inside a terminal")
	}
	size := func() (int, int, error) { return term.GetSize(int(os.Stdout.Fd())) }
	w, h, err := size()
	if err != nil || w < 1 || h < 2 {
		return errors.New("cannot read terminal size")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}

	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	out.WriteString("\x1b[?1049h\x1b[?25l")
	if cfg.Mouse {
		out.WriteString("\x1b[?1002h\x1b[?1006h")
	}
	if cfg.UseKitty() {
		out.WriteString("\x1b[>5u")
	}
	out.Flush()

	m := mux.New(cfg, out)
	cleanup := func() {
		m.Shutdown()
		if cfg.UseKitty() {
			out.WriteString("\x1b[<u")
		}
		if cfg.Mouse {
			out.WriteString("\x1b[?1006l\x1b[?1002l")
		}
		out.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l")
		out.Flush()
		_ = term.Restore(fd, old)
	}
	defer func() {
		if r := recover(); r != nil {
			cleanup()
			panic(r)
		}
	}()

	m.SetSize(w, h)
	m.Start()

	keys := make(chan []input.Key, 16)
	go readInput(keys)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)

	m.Loop(keys, winch, stop, size)
	cleanup()
	return nil
}
