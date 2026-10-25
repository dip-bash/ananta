// Command ananta is a scrollable tiling terminal multiplexer with
// persistent sessions.
//
//	ananta                 attach to session "main" (creating it if needed)
//	ananta new [name]      create a session and attach
//	ananta attach [name]   attach to an existing session
//	ananta ls              list sessions
//	ananta kill [name]     end a session
//	ananta local           run in this terminal only (no background session)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"ananta/internal/app"
	"ananta/internal/config"
	"ananta/internal/session"
)

const usage = `usage: ananta [-config file] [-s name] [command [name]]

commands:
  (none)         attach to session "main", creating it if needed
  new [name]     create a session and attach
  attach [name]  attach to an existing session
  ls             list sessions
  kill [name]    end a session
  local          run in this terminal only (nothing survives closing it)
  init           write the default config to ~/.config/ananta/config.toml
  print-config   print the default config
`

func main() {
	cfgFlag := flag.String("config", "", "path to config file (default ~/.config/ananta/config.toml)")
	sessFlag := flag.String("s", "main", "session name")
	initFlag := flag.Bool("init", false, "write the default config file and exit")
	printFlag := flag.Bool("print-config", false, "print the default config and exit")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	cmd := "default"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	name := *sessFlag
	if len(args) > 0 {
		name = args[0]
	}
	if *initFlag {
		cmd = "init"
	}
	if *printFlag {
		cmd = "print-config"
	}
	cfgPath := config.Path(*cfgFlag)

	var err error
	switch cmd {
	case "print-config":
		fmt.Print(config.Default)
	case "init":
		err = writeDefault(cfgPath)
	case "ls":
		err = list()
	case "kill":
		err = checkName(name)
		if err == nil {
			err = session.Kill(name)
		}
	case "attach":
		if err = checkName(name); err == nil {
			err = session.Attach(name)
		}
	case "default", "new":
		if err = checkName(name); err != nil {
			break
		}
		if cmd == "new" && session.Exists(name) {
			err = fmt.Errorf("session %q already exists (use `ananta attach %s`)", name, name)
			break
		}
		if _, err = config.Load(cfgPath); err != nil { // report config errors here, not in a hidden daemon
			break
		}
		if err = session.EnsureServer(name, *cfgFlag); err == nil {
			err = session.Attach(name)
		}
	case "local":
		var cfg *config.Config
		if cfg, err = config.Load(cfgPath); err == nil {
			err = app.Run(cfg)
		}
	case "server": // internal: started by EnsureServer
		var cfg *config.Config
		if err = checkName(name); err == nil {
			if cfg, err = config.Load(cfgPath); err == nil {
				err = session.Serve(cfg, name)
			}
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ananta:", err)
		os.Exit(1)
	}
}

func checkName(name string) error {
	if !session.ValidName(name) {
		return fmt.Errorf("invalid session name %q (use letters, digits, . _ -)", name)
	}
	return nil
}

func list() error {
	infos, err := session.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, in := range infos {
		state := "detached"
		if in.Attached {
			state = "attached"
		}
		fmt.Printf("%-16s %2d panes  %s  (pid %d)\n", in.Name, in.Panes, state, in.PID)
	}
	return nil
}

func writeDefault(path string) error {
	if path == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("already exists: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(config.Default), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}
