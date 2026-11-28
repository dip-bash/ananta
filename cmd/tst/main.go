// Command tst is a scrollable tiling terminal multiplexer with
// persistent sessions.
//
//	tst                 attach to session "main" (creating it if needed)
//	tst new [name]      create a session and attach
//	tst attach [name]   attach to an existing session
//	tst ls              list sessions
//	tst kill [name]     end a session
//	tst local           run in this terminal only (no background session)
//	tst open "htop" -n 3 -w 33% -s test   control a running session (see tst help)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"tst/internal/app"
	"tst/internal/config"
	"tst/internal/session"
	"tst/internal/vault"
)

var version = "dev"

const usage = `
	Usage:
	  tst [OPTIONS] [COMMAND] [ARGS]

	Options:
	  -config FILE    use a custom configuration file
	  -s NAME         select a session (default: main)

	Session commands:
	  (none)          attach to session "main", creating it if needed
	  new [NAME]      create and attach to a session
	  attach [NAME]   attach to an existing session
	  ls              list available sessions
	  kill [NAME]     terminate a session
	  local           run locally without session persistence
	  vault           open the vault in this terminal

	Configuration:
	  init            generate default configuration files
	  print-config    print the default config
	  print-wallpaper print the default wallpaper

	Session controls:
	  open "CMD" [-n WORKSPACE] [-w WIDTH] [-s SESSION]
					  open a program in a new pane ("vault" = the vault UI)
	  workspace N     switch workspace
	  focus DIR       focus a pane in a direction
	  width W         set pane width
	  send "TEXT"     send text to a pane
	  close           close the current pane
	  list            list panes
	  action NAME     execute an action (mark, unmark, vault, ...)

	Notes:
	  - Commands control the current session by default.
	  - If not inside a session, commands default to "main".
	  - Program arguments must be enclosed in double quotes.

	Run 'tst help' for more information.

`

func main() {
	cfgFlag := flag.String("config", "", "path to config file (default ~/.config/tst/config.toml)")
	sessFlag := flag.String("s", "main", "session name")
	initFlag := flag.Bool("init", false, "write the default config file and exit")
	printFlag := flag.Bool("print-config", false, "print the default config and exit")
	printVersion := flag.Bool("v", false, "print the version and exit")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	explicitSession := ""
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "s" {
			explicitSession = f.Value.String()
		}
	})
	args := flag.Args()
	cmd := "default"
	var rest []string
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
		rest = args
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
	if *printVersion {
		cmd = "print-version"
	}
	cfgPath := config.Path(*cfgFlag)
	if cfgPath != "" {
		if abs, err := filepath.Abs(cfgPath); err == nil {
			cfgPath = abs
		}
	}

	var err error
	switch cmd {
	case "print-config":
		fmt.Print(config.Default)
	case "print-wallpaper":
		fmt.Print(config.DefaultWallpaper)
	case "init":
		err = writeDefault(cfgPath)
	case "print-version":
		fmt.Printf("T-Strip Version: %s\n", version)
	case "help":
		fmt.Print(usage, "\n", controlUsage)
	case "ls":
		err = list()
	case "kill":
		err = checkName(name)
		if err == nil {
			err = session.Kill(name)
		}
	case "attach":
		if err = checkName(name); err == nil {
			pane := 0
			if len(rest) > 1 {
				if n, e := strconv.Atoi(rest[1]); e == nil {
					pane = n
				}
			}
			err = session.AttachPane(name, pane)
		}
	case "default", "new":
		if err = checkName(name); err != nil {
			break
		}
		if cmd == "new" && session.Exists(name) {
			err = fmt.Errorf("session %q already exists (use `tst attach %s`)", name, name)
			break
		}
		if _, err = config.Load(cfgPath); err != nil { // report config errors here, not in a hidden daemon
			break
		}
		if err = session.EnsureServer(name, cfgPath); err == nil {
			err = session.Attach(name)
		}
	case "local":
		var cfg *config.Config
		if cfg, err = config.Load(cfgPath); err == nil {
			err = app.RunWith(cfg, cfgPath)
		}
	case "vault":
		var cfg *config.Config
		if cfg, err = config.Load(cfgPath); err == nil {
			// outside tst: boot the default session and show the empty vault
			bootErr := ""
			if os.Getenv("TST_SESSION") == "" && os.Getenv("TST") != "1" {
				if e := session.EnsureServer("main", cfgPath); e != nil {
					bootErr = e.Error()
				}
			}
			err = vault.Run(cfg, vault.Options{BootErr: bootErr, ConfigPath: cfgPath})
		}
	case "server": // internal: started by EnsureServer
		var cfg *config.Config
		if err = checkName(name); err == nil {
			if cfg, err = config.Load(cfgPath); err == nil {
				err = session.ServeWith(cfg, name, cfgPath)
			}
		}
	default:
		if controlCommands[cmd] {
			// flags and arguments after the command belong to the command
			err = runControl(cmd, rest, explicitSession)
			break
		}
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tst:", err)
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

// writeDefault creates config.toml and wallpaper.toml (whichever are missing).
func writeDefault(path string) error {
	if path == "" {
		return fmt.Errorf("cannot determine config directory")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct{ path, text string }{
		{path, config.Default},
		{filepath.Join(dir, config.DefaultWallpaperFile), config.DefaultWallpaper},
	}
	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			fmt.Println("kept   ", f.path, "(already exists)")
			continue
		}
		if err := os.WriteFile(f.path, []byte(f.text), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote  ", f.path)
	}
	return nil
}
