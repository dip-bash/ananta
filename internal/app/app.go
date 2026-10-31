// Package app runs ananta in the current terminal with no background session
// ("local" mode: closing the terminal ends everything). The persistent
// server/client mode lives in package session.
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

// readInput feeds terminal input to the mux, reassembling escape sequences
// that a read boundary cut in half. onEOF runs when stdin closes.
func readInput(keys chan<- []input.Key, onEOF func()) {
	asm := input.NewAssembler(func(ks []input.Key) { keys <- ks })
	buf := make([]byte, 8192)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			asm.Feed(buf[:n])
		}
		if err != nil {
			asm.Close()
			onEOF()
			return
		}
	}
}

// Run starts the multiplexer in this terminal and blocks until it exits.
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

	setup, teardown := cfg.TermModes()
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	out.WriteString(setup)
	out.Flush()

	m := mux.New(cfg, out)
	cleanup := func() {
		m.Shutdown()
		out.WriteString(teardown)
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
	m.QueryHostColors()

	keys := make(chan []input.Key, 256)
	post := make(chan func(), 16)
	go readInput(keys, func() { post <- m.Quit })
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-winch:
				if w, h, err := size(); err == nil {
					post <- func() { m.SetSize(w, h) }
				}
			case <-stop:
				post <- m.Quit
			}
		}
	}()

	m.Loop(keys, post)
	cleanup()
	return nil
}
