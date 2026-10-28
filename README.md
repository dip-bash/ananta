# ananta

<img width="2048" height="768" alt="Ananta" src="https://github.com/user-attachments/assets/21e5e7a5-144b-4adb-a719-8b5648783bf1" />

A scrollable tiling terminal multiplexer written in Go. Panes live in columns
on an endless horizontal strip; focusing a column glides the view to it.
Square borders, animated scrolling and zoom, an overview of every workspace,
a configurable status bar, a live wallpaper, mouse support, per-pane
scrollback, a keyboard copy mode, and persistent sessions (detach / reattach).

## Build

    go mod tidy
    make build              # or: go build -o ananta ./cmd/ananta
    ./ananta init           # writes ~/.config/ananta/config.toml and wallpaper.toml
    ./ananta                # attach to session "main" (created on first use)

ananta starts empty: press `leader+enter` (alt+enter by default) to open the
first terminal. Closing every pane does not end ananta; only `leader+Q` quits
(and, in a session, ends it). `make cross-build` produces linux x86_64 and
aarch64 binaries.
`./ananta print-config` prints every option with comments.

## Sessions (detach / reattach)

A background server owns every pane, so your programs keep running when the
terminal closes or SSH drops. The terminal you type in is just a client.

    ananta                 attach to session "main" (creating it if needed)
    ananta -s work         ... or any other name
    ananta new work        create + attach (errors if it exists)
    ananta attach work     attach to an existing session
    ananta ls              list sessions (panes, attached / detached)
    ananta kill work       end a session and everything in it
    ananta local           no background session; closing the terminal ends it

A new session starts with no panes; it keeps running with zero panes until
`leader+Q`. Detach with `leader+d`; attaching from a second terminal takes the session
over and re-lays it out for the new size. `leader+Q` ends the session. The
session name is shown in the status bar (the `session` widget; remove it from
the bar lists to hide it).

Sockets and server logs live in `$XDG_RUNTIME_DIR/ananta-<uid>/` (or the temp
dir). The server reads the config when it starts: restart the session to apply
changes. Panes started later inherit the environment the server was launched
with, so e.g. a changed `SSH_AUTH_SOCK` is not picked up.

## Controlling a session from the command line

Any shell, script or keybinding can drive a running session. The program to
run goes in **double quotes, as one argument**:

    ananta open "htop" -n 3 -w 0.33 -s test    # htop, workspace 3, 33% wide, session "test"
    ananta open "tail -f /var/log/syslog" --below
    ananta open "make watch" -k                # -k: keep keyboard focus where it is
    ananta open                                # no program: a new shell

    ananta workspace 3                         # switch the view
    ananta focus left                          # left | right | up | down
    ananta width 50%                           # resize the focused column
    ananta send -e "make test"                 # type into a pane (-e presses Enter)
    ananta close                               # close the focused pane
    ananta list                                # id, workspace, column, width, program
    ananta action overview                     # run any action by name,
    ananta action goto_workspace 2             # exactly as if its key was pressed

Widths are a fraction or a percentage: `0.33`, `33%` and `33` all mean the same
(a column is never narrower than 12 cells). Flags may go before or after the
program. `open` prints the new pane's id; pass it to `-p ID` on `width`,
`send` and `close` to target that pane.

**Which session?** `-s NAME` if given; otherwise the session you are inside
(every pane has `ANANTA_SESSION` and `ANANTA_PANE` set, so a command typed in a
pane of session "work" acts on "work", and `close` / `send` / `width` default to
that very pane); otherwise `main`. Inside `ananta local` there is no session,
so name one with `-s`.

`open` starts the program in the directory you ran the command from (`-d DIR`
to choose another). A pane opened in the current workspace takes keyboard focus
like any new pane; one opened in another workspace leaves your view alone
unless you add `--focus`.

## Wallpaper

The empty part of a workspace is a canvas. The wallpaper is a program that
runs behind your panes and is drawn wherever no pane covers the screen: an
empty workspace, the space beside a narrow column, every row of the overview.
It is display-only: it never receives keys or mouse input.

In `config.toml`:

    wallpaper = "default"        # wallpaper.toml next to config.toml
    wallpaper = "matrix.toml"    # any other file from the same folder
    wallpaper = "none"           # off

A wallpaper file is a few lines of TOML (see `wallpaper.toml`, written by
`ananta init`):

    command = "htop"             # or a script, or a multi-line shell script
    dir     = ""                 # working directory (default: home)
    dim     = true               # draw faint so panes stand out
    restart = true               # start again after 2 s if it exits

The command runs through `sh -c` on a terminal the size of the screen. With no
`wallpaper.toml` the built-in clock is used.

## Scrollback

Every pane keeps `scrollback_lines` (default 10000) of history. Scroll with
the mouse wheel, `shift+pageup` / `shift+pagedown`, or copy mode. Typing
returns to the live screen. Dragging a selection past the top or bottom edge
scrolls. Full-screen programs use the alternate screen, which has no history;
the wheel sends them arrow keys instead.

## Clipboard from inside panes

Programs in a pane can set your clipboard with OSC 52
(`printf '\e]52;c;%s\a' "$(printf hi | base64)"`). ananta forwards it to your
terminal and to the native clipboard tool, so copying inside a remote editor
lands in your local clipboard. Disable with `app_clipboard = false`.
New panes start in the directory of the pane you opened them from.

## Leader key

Default shortcuts are written `leader+<key>`. Change one line to move them all:

    leader = "alt"        # or "super", "ctrl", "ctrl+alt", ...

`super` / `ctrl` need a terminal that supports the extended (CSI-u) keyboard
protocol. Plain terminals cannot send super, and ctrl+h/i/j/m collide with
backspace/tab/enter, so `alt` is the safe default.

## Keys (leader = alt)

| Key | Action |
|---|---|
| leader+h/j/k/l, leader+arrows | focus left / down / up / right |
| leader+H/J/K/L | move column / pane |
| leader+enter / leader+s | new column / stack new pane below |
| leader+w | close pane |
| leader+, / leader+. | stack into left column / pop out |
| leader+r / f / - / = | cycle width / maximize / narrow / widen |
| leader+c | toggle always-centered scrolling |
| leader+o | overview |
| leader+b | show / hide status bar |
| leader+1..9 | go to workspace |
| leader+! @ # $ % ^ & * ( | move pane to workspace 1..9 |
| leader+/ | help (scrollable, generated from your config) |
| leader+d | detach |
| leader+Q | quit |
| alt+a | copy mode |
| shift+pageup / shift+pagedown | scroll through history |

## Status bar

    [bar]
    position = "bottom"      # top | bottom | hidden
    left   = ["session", "workspaces", "columns", "mode"]
    center = ["message"]
    right  = ["cpu", "mem", "net", "battery", "time", "help"]
    time_format = "15:04"    # Go layout

Built-in widgets: session workspaces columns mode message title help cpu mem
net battery load time date host user. Anything else is a shell command:

    [bar.widgets.weather]
    command  = "curl -s 'wttr.in/?format=%t'"
    interval = 600
    prefix   = "☀ "

then add `"weather"` to a list. On narrow screens right-hand widgets drop out
from the left. cpu/mem/net/battery read `/proc` and `/sys` (Linux); where those
are unavailable they hide, and custom commands still work.

## Mouse and selection

- Click a pane to focus it. In the overview a click focuses, zooms in and centers.
- Drag inside a pane to select text. The selection never leaves that pane (no
  borders, no neighbours) and is copied on release (OSC 52 plus the native
  clipboard tool when one is installed).
- Programs that use the mouse themselves keep receiving it; hold shift to force
  ananta's selection over such a program.
- Wheel: scrolls history; switches workspaces in the overview.

## Copy mode (alt+a)

Moves a cursor over the pane's screen and its whole history: `h j k l`, `w b e`,
`W B E`, `0 ^ $`, `gg G`, `H M L`, `{ }`, `f F t T ; ,`, `ctrl+u/d/f/b`, counts
(`3j`). `v` / `V` / `ctrl+v` start character / line / block selection, `o`
swaps ends, `y` or `Enter` copies, `Esc` leaves.

## Project layout

    cmd/ananta/main.go      flags, commands, config loading
    cmd/ananta/control.go   `ananta open|workspace|focus|width|send|close|list|action`
    internal/app/           "local" mode: runs the mux directly in this terminal
    internal/session/       persistent sessions: server, client, wire protocol
    internal/config/        config structs, loader, embedded default config + wallpaper, action table
    internal/input/         key / mouse parsing, chord normalisation, input assembler
    internal/screen/        cell buffer + diffing renderer
    internal/width/         display width of text (CJK / emoji take 2 cells)
    internal/vt/            terminal emulator with scrollback (derived from an MIT-licensed
                            emulator, see internal/vt/LICENSE)
    internal/pane/          one PTY + its emulator
    internal/stats/         cpu / mem / net / battery sampling, command widgets
    internal/clipboard/     OSC 52 + native clipboard tools
    internal/mux/           the multiplexer
        mux.go              types, construction, event loop
        layout.go           workspaces / columns / panes, scrolling math, animation
        actions.go          what each action does          <- add new actions here
        keyboard.go         key dispatch
        mouse.go            clicks, drag selection, wheel, forwarding to programs
        render.go           frame composition, overview thumbnails
        control.go          commands from outside (open, workspace, send, list, ...)
        wallpaper.go        the background program
        bar.go              status bar + widgets            <- add new widgets here
        help.go             scrollable help overlay
        selection.go        selection model, text extraction, copy
        scrollback.go       logical rows (history + screen), viewport anchoring
        copymode.go         keyboard copy mode

Adding an action: add a line to `ActionInfo` (internal/config/actions.go), a
`case` in `run` (internal/mux/actions.go), and bind it in `default.toml`. The
help screen picks it up automatically.

## Terminal compatibility

ananta's emulator answers the usual identification queries (primary and
secondary device attributes, terminal version, cursor position) so shells that
probe the terminal at startup, such as fish 4, get their answer immediately
instead of timing out.

## Known limits

History is not re-wrapped when a pane is resized; one attached client per
session at a time; combining marks are not drawn (the base character is);
the wallpaper is shared by all workspaces.
