# ananta

A scrollable tiling terminal multiplexer in Go (niri / PaperWM style). Panes
live in columns on an endless horizontal strip; focusing a column glides the
viewport to it. Square borders, animated scrolling and zoom, overview,
configurable status bar, mouse, per-pane scrollback, vim-style copy mode and
tmux-style persistent sessions (detach / reattach).

## Build

    go mod tidy
    make build              # or: go build -o ananta ./cmd/ananta
    ./ananta --init         # writes ~/.config/ananta/config.toml
    ./ananta                # attach to session "main" (created on first use)

`./ananta --print-config` prints every option with comments.

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

Detach with `leader+d` (alt+d); attaching from a second terminal takes the
session over (and re-lays it out for the new size). `leader+Q` ends the session.
Sockets and server logs live in `$XDG_RUNTIME_DIR/ananta-<uid>/` (or the temp
dir). The server reads the config when it starts: restart the session to apply
config changes. Panes started later inherit the environment the server was
launched with (as in tmux), so e.g. a changed SSH_AUTH_SOCK is not picked up.

## Clipboard from inside panes

Programs running in a pane can set your clipboard with OSC 52 (neovim, helix,
tmux inside a pane, `printf '\e]52;c;%s\a' "$(printf hi | base64)"`). ananta
forwards it to your terminal and to the native clipboard tool, so yanking in
vim over SSH lands in your local clipboard. Disable with `app_clipboard = false`.
New panes start in the directory of the pane you opened them from.

## Scrollback

Every pane keeps `scrollback_lines` (default 10000) of history, including long
output such as `tree`. Scroll with the mouse wheel, `shift+pageup` /
`shift+pagedown`, or copy mode (`alt+a`, then `gg`, `ctrl+u`, `/`-less vim
motions all work across the whole history). Typing jumps back to the live
screen. Dragging a selection past the top/bottom edge auto-scrolls. Full-screen
apps (vim, less) use the alternate screen and have no history, as in other
terminals; the wheel sends them arrow keys instead.

## Project layout

    cmd/ananta/main.go      flags, config loading, starts the app
    internal/app/           "local" mode: runs the mux directly in this terminal
    internal/config/        config structs, loader, default.toml (embedded), action table
    internal/input/         key / mouse parsing, chord normalisation (leader expansion)
    internal/screen/        cell buffer + diffing renderer
    internal/width/         display width of text (CJK / emoji take 2 cells)
    internal/vt/            terminal emulator: vendored vt10x (MIT) + scrollback ring
    internal/pane/          one PTY + its emulator
    internal/session/       persistent sessions: server (owns the mux), client, wire protocol
    internal/stats/         cpu / mem / net / battery sampling, custom command widgets
    internal/clipboard/     OSC 52 + native clipboard tools
    internal/mux/           the multiplexer
        mux.go              types, construction, event loop
        layout.go           workspaces / columns / panes, scrolling math, animation
        actions.go          what each action does          <- add new actions here
        keyboard.go         key dispatch
        mouse.go            clicks, drag selection, wheel, app mouse forwarding
        render.go           frame composition, overview thumbnails
        bar.go              status bar + widgets            <- add new widgets here
        help.go             scrollable help overlay
        selection.go        selection model, text extraction, copy / paste
        scrollback.go       logical rows (history + screen), viewport anchoring
        copymode.go         vim visual copy mode

Adding an action: add a line to `ActionInfo` (internal/config/actions.go), a
`case` in `run` (internal/mux/actions.go), and bind it in `default.toml`.
The help screen picks it up automatically.

## Leader key

Default shortcuts are written `leader+<key>`. Change one line to move them all:

    leader = "alt"        # or "super", "ctrl", "ctrl+alt", ...

`super` / `ctrl` need a terminal with the kitty keyboard protocol (kitty, foot,
wezterm, ghostty, alacritty >= 0.13, ...). Plain terminals cannot send super,
and ctrl+h/i/j/m collide with backspace/tab/enter, so `alt` is the safe default.

## Keys (leader = alt)

| Key | Action |
|---|---|
| leader+h/j/k/l, leader+arrows | focus left / down / up / right |
| leader+H/J/K/L | move column / pane |
| leader+enter / leader+s | new column / stack new pane below |
| leader+w | close pane |
| leader+, / leader+. | stack into left column / pop out |
| leader+r / f / - / = | cycle width / maximize / narrow / widen |
| leader+c | toggle always-centered <-> PaperWM-style scrolling |
| leader+C | center the focused column once |
| leader+o | overview |
| leader+b | show / hide status bar |
| leader+p | paste last copied text |
| leader+1..9 | workspace (leader+! @ # move pane to 1-3) |
| leader+/ | help (scrollable, generated from your config) |
| leader+Q | quit |
| alt+a | vim visual copy mode |
| leader+d | detach (session keeps running) |
| shift+pageup / shift+pagedown | scroll through history |

## Status bar

    [bar]
    position = "bottom"      # top | bottom | hidden
    left   = ["workspaces", "columns", "mode"]
    center = ["message"]
    right  = ["cpu", "mem", "net", "battery", "time", "help"]
    time_format = "15:04"    # Go layout

Built-in widgets: workspaces columns mode message title help cpu mem net
battery load time date host user. Anything else is a shell command:

    [bar.widgets.weather]
    command  = "curl -s 'wttr.in/?format=%t'"
    interval = 600
    prefix   = "☀ "

then add `"weather"` to a list. On narrow screens right-hand widgets drop out
from the left. cpu/mem/net/battery read /proc and /sys (Linux); where those are
unavailable (macOS, locked-down Android) they hide, and custom commands still
work (e.g. `termux-battery-status`).

## Mouse and selection

- Click a pane to focus it. In the overview a click focuses, zooms in and centers.
- Drag inside a pane to select text. The selection never leaves that pane (no
  borders, no neighbours) and is copied on release (OSC 52 + wl-copy / xclip /
  xsel / termux-clipboard-set / pbcopy when installed).
- Apps that use the mouse themselves (vim, htop) keep receiving it; hold shift
  to force ananta's selection over such an app.
- Wheel: scrolls full-screen apps (less, vim); switches workspaces in the overview.

## Copy mode (alt+a)

Moves a cursor over the pane's screen. `hjkl` `w b e` `W B E` `0 ^ $` `gg G`
`H M L` `{ }` `f F t T ; ,` `ctrl+u/d/f/b`, counts (`3j`); `v` / `V` /
`ctrl+v` start char / line / block selection, `o` swaps ends, `y` or `Enter`
copies, `Esc` leaves. It works over the whole scrollback, not just the screen.

## Not included yet

History is not re-wrapped when a pane is resized; one attached client per
session at a time; programs' OSC 10/11 colour queries are not answered;
combining marks are not drawn (the base character is).
