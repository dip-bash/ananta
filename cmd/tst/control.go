package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"tst/internal/mux"
	"tst/internal/session"
)

// Commands that act on a running session.
var controlCommands = map[string]bool{
	"open": true, "run": true, "workspace": true, "focus": true, "width": true,
	"close": true, "send": true, "list": true, "action": true,
}

const controlUsage = `
	Control a running session from anywhere (shell, script, or keybinding):

	  tst open "htop" -n 3 -w 33% -s test    open htop in workspace 3 at 33% width, session "test"
	  tst open "tail -f /var/log/syslog" --below
	  tst open "make watch" -k               keep keyboard focus on the current pane
	  tst open "htop" --focus -n 3           also switch the view to workspace 3
	  tst open                               open a new shell
	  tst workspace 3                       switch the view to workspace 3
	  tst focus left|right|up|down           move focus
	  tst width 50%                          set the focused column's width
	  tst send -e "make test"                send text to the focused pane (-e presses Enter)
	  tst close                              close the focused pane
	  tst list                               list panes (ID, workspace, column, width)
	  tst action overview                    run an action, as if its key was pressed
	  tst action goto_workspace 2            run an action with arguments

	  -k / --keep-focus                         don't move keyboard focus to the new pane
	  --focus                                   switch the view to the target workspace

	The program to run must be enclosed in double quotes as ONE argument.
	Pane commands accept -p ID to target a pane (see "tst list").

	Which session: -s NAME, else the session you are inside (when run from a
	pane of T-Strip), else "main".
`

// resolveSession picks the session a control command talks to.
// explicit comes from -s; otherwise the session this process runs inside
// (set by tst in every pane); otherwise "main".
func resolveSession(explicit string) (name string, fromEnv bool, err error) {
	if explicit != "" {
		return explicit, false, nil
	}
	if env := os.Getenv("TST_SESSION"); env != "" {
		return env, true, nil
	}
	if os.Getenv("TST") == "1" {
		return "", false, fmt.Errorf("this terminal runs in `tst local` mode, which has no session to control; use -s NAME to target one")
	}
	return "main", false, nil
}

// parseInterleaved parses flags that may appear before, between or after the
// positional arguments (Go's flag package stops at the first positional).
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	for i, a := range args {
		if a == "--" { // everything after is positional
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	return append(pos, tail...), nil
}

// runControl executes `tst <cmd> ...`. globalSession is the -s given before
// the command name (empty if none).
func runControl(cmd string, args []string, globalSession string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		sessName  = globalSession
		workspace int
		width     string
		dir       string
		below     bool
		focus     bool
		keepFocus bool
		paneID    int
		enter     bool
	)
	strFlag := func(p *string, short, long, _ string) { fs.StringVar(p, short, *p, ""); fs.StringVar(p, long, *p, "") }
	strFlag(&sessName, "s", "session", "")
	fs.IntVar(&paneID, "p", 0, "")
	fs.IntVar(&paneID, "pane", 0, "")
	switch cmd {
	case "open", "run":
		fs.IntVar(&workspace, "n", 0, "")
		fs.IntVar(&workspace, "workspace", 0, "")
		strFlag(&width, "w", "width", "")
		strFlag(&dir, "d", "dir", "")
		fs.BoolVar(&below, "below", false, "")
		fs.BoolVar(&focus, "focus", false, "")
		fs.BoolVar(&keepFocus, "keep-focus", false, "")
		fs.BoolVar(&keepFocus, "k", false, "")
	case "send":
		fs.BoolVar(&enter, "e", false, "")
		fs.BoolVar(&enter, "enter", false, "")
	}
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return fmt.Errorf("%v\n\n%s", err, controlUsage)
	}

	name, fromEnv, err := resolveSession(sessName)
	if err != nil {
		return err
	}
	// inside a pane, commands default to that pane (like the focused one, but exact)
	if paneID == 0 && fromEnv {
		if v, err := strconv.Atoi(os.Getenv("TST_PANE")); err == nil && (cmd == "close" || cmd == "send" || cmd == "width") {
			paneID = v
		}
	}

	req := mux.Request{Op: cmd, Pane: paneID}
	need := func(n int, what string) error {
		if len(pos) != n {
			return fmt.Errorf("%s: expected %s\n\n%s", cmd, what, controlUsage)
		}
		return nil
	}
	switch cmd {
	case "open", "run":
		req.Op = "open"
		if len(pos) > 1 {
			return fmt.Errorf("put the program and its arguments in double quotes, as one argument:\n  tst open %q", strings.Join(pos, " "))
		}
		if len(pos) == 1 {
			req.Command = pos[0]
		}
		req.Workspace, req.Below, req.Focus, req.KeepFocus = workspace, below, focus, keepFocus
		if width != "" {
			if req.Width, err = mux.ParseWidth(width); err != nil {
				return err
			}
		}
		if req.Dir = dir; dir == "" {
			req.Dir, _ = os.Getwd() // start where you ran the command
		} else if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("directory %q does not exist", dir)
		}
	case "workspace":
		if err := need(1, "a workspace number"); err != nil {
			return err
		}
		if req.Workspace, err = strconv.Atoi(pos[0]); err != nil || req.Workspace < 1 {
			return fmt.Errorf("workspace must be a number from 1 to 9")
		}
	case "focus":
		if err := need(1, "left, right, up or down"); err != nil {
			return err
		}
		req.Direction = pos[0]
	case "width":
		if err := need(1, "a width such as 0.33 or 33%"); err != nil {
			return err
		}
		if req.Width, err = mux.ParseWidth(pos[0]); err != nil {
			return err
		}
	case "close", "list":
		if err := need(0, "no arguments"); err != nil {
			return err
		}
	case "send":
		if err := need(1, "the text to send, in double quotes"); err != nil {
			return err
		}
		req.Text, req.Enter = pos[0], enter
	case "action":
		if len(pos) < 1 {
			return fmt.Errorf("action: expected an action name\n\n%s", controlUsage)
		}
		req.Action, req.Arg = pos[0], strings.Join(pos[1:], " ")
	}

	out, err := session.Control(name, req)
	if err != nil {
		return err
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}
