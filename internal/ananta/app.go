package ananta

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/term"
)

func readInput(in io.Reader, keys chan<- []Key) {
	buf := make([]byte, 8192)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			keys <- parseKeys(data)
		}
		if err != nil {
			close(keys)
			return
		}
	}
}

func Run(in *os.File, outFile, errOut io.Writer, args []string) error {
	fs := flag.NewFlagSet("ananta", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgFlag := fs.String("config", "", "path to config file (default ~/.config/ananta/config.toml)")
	initFlag := fs.Bool("init", false, "write the default config file and exit")
	printFlag := fs.Bool("print-config", false, "print the default config and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *printFlag {
		_, _ = fmt.Fprint(outFile, defaultConfig)
		return nil
	}
	path := configPath(*cfgFlag)
	if *initFlag {
		if path == "" {
			return fmt.Errorf("cannot determine config directory")
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("already exists: %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(outFile, "wrote", path)
		return nil
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	fd := int(in.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("ananta must run in a terminal")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	out := bufio.NewWriterSize(outFile, 1<<16)
	_, _ = out.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h")
	_ = out.Flush()

	m := newMux(cfg, out)
	cleanup := func() {
		m.shutdown()
		_, _ = out.WriteString("\x1b[?1006l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
		_ = out.Flush()
		_ = term.Restore(fd, old)
	}
	defer func() {
		if r := recover(); r != nil {
			cleanup()
			panic(r)
		}
	}()

	m.onResize()
	if m.W == 0 {
		cleanup()
		return fmt.Errorf("cannot read terminal size")
	}
	m.applyInitialLayout()

	keys := make(chan []Key, 16)
	go readInput(in, keys)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	term2 := make(chan os.Signal, 1)
	signal.Notify(term2, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(term2)

	m.loop(keys, winch, term2)
	cleanup()
	return nil
}
