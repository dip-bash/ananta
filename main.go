package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/term"
)

func readInput(keys chan<- []Key) {
	buf := make([]byte, 8192)
	for {
		n, err := os.Stdin.Read(buf)
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

func main() {
	cfgFlag := flag.String("config", "", "path to config file (default ~/.config/scrollmux/config.toml)")
	initFlag := flag.Bool("init", false, "write the default config file and exit")
	printFlag := flag.Bool("print-config", false, "print the default config and exit")
	flag.Parse()

	if *printFlag {
		fmt.Print(defaultConfig)
		return
	}
	path := configPath(*cfgFlag)
	if *initFlag {
		if path == "" {
			fmt.Fprintln(os.Stderr, "cannot determine config directory")
			os.Exit(1)
		}
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintln(os.Stderr, "already exists:", path)
			os.Exit(1)
		}
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
		return
	}

	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintln(os.Stderr, "scrollmux must run in a terminal")
		os.Exit(1)
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	out.WriteString("\x1b[?1049h\x1b[?25l")
	if cfg.Mouse {
		out.WriteString("\x1b[?1002h\x1b[?1006h")
	}
	if cfg.useKitty() {
		out.WriteString("\x1b[>5u")
	}
	out.Flush()

	m := newMux(cfg, out)
	cleanup := func() {
		m.shutdown()
		if cfg.useKitty() {
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

	m.onResize()
	if m.W == 0 {
		cleanup()
		fmt.Fprintln(os.Stderr, "cannot read terminal size")
		os.Exit(1)
	}
	m.newPane("")

	keys := make(chan []Key, 16)
	go readInput(keys)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	term2 := make(chan os.Signal, 1)
	signal.Notify(term2, syscall.SIGTERM, syscall.SIGHUP)

	m.loop(keys, winch, term2)
	cleanup()
}
