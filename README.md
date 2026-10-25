# ananta

A scrollable tiling terminal multiplexer in Go (niri / PaperWM style). Panes
live in columns on an endless horizontal strip; focusing a column glides the
viewport to it. Animated scrolling and zoom, an overview mode, configurable
status bar, mouse, per-pane scrollback, vim-style copy mode - plus tmux-style
persistent sessions: a background server keeps every program running when you
close the terminal or drop SSH, and `ananta` reattaches.

**Full documentation lives in the [wiki](https://github.com/dip-bash/ananta/wiki)**
- key bindings, configuration reference, sessions, copy mode, status bar,
  architecture, FAQ and troubleshooting.

## Build

    go mod tidy
    make build              # or: go build -o ananta ./cmd/ananta
    ./ananta --init         # writes ~/.config/ananta/config.toml
    ./ananta                # attach to session "main" (created on first use)

`./ananta --print-config` prints every config option with comments.

## Quick start

| Command | Does |
|---|---|
| `ananta` | attach to session "main", creating it if needed |
| `ananta -s work` | same, under session name `work` |
| `ananta new work` | create + attach (errors if it exists) |
| `ananta attach work` | attach to an existing session |
| `ananta ls` | list live sessions |
| `ananta kill work` | end a session and everything in it |
| `ananta local` | run in this terminal only, nothing survives it |

Detach with **alt+d** - the session keeps running; reattach from anywhere.
End a session with **alt+Q** or `ananta kill <name>`.

## The ten chords you need (leader = alt)

| Key | Action |
|---|---|
| `alt+h j k l` / arrows | focus left / right / down / up |
| `alt+enter` / `alt+s` | new column / stack pane below |
| `alt+w` | close pane |
| `alt+H J K L` | move column / pane |
| `alt+1..9` | workspace |
| `alt+o` / `alt+f` | overview / maximize column |
| `alt+a` | copy mode (vim motions over all history) |
| `alt+p` | paste |
| `alt+d` / `alt+Q` | detach / quit |
| `alt+/` | scrollable help, generated from your config |

Everything else is forwarded to the app. All bindings live in
`~/.config/ananta/config.toml` - see
[Key Bindings](https://github.com/dip-bash/ananta/wiki/Key-Bindings) for the
complete table and [Configuration](https://github.com/dip-bash/ananta/wiki/Configuration)
for every option (leader key, themes, status-bar widgets, prefix mode, ...).

## Docs

| Wiki page | Covers |
|---|---|
| [Home](https://github.com/dip-bash/ananta/wiki/Home) | overview of all pages |
| [Getting Started](https://github.com/dip-bash/ananta/wiki/Getting-Started) | first run, detach, reattach |
| [Key Bindings](https://github.com/dip-bash/ananta/wiki/Key-Bindings) | every default chord, rebinding, prefix mode |
| [Sessions](https://github.com/dip-bash/ananta/wiki/Sessions) | server model, `ls` / `kill`, sockets and logs |
| [Workspaces and Layout](https://github.com/dip-bash/ananta/wiki/Workspaces-and-Layout) | columns, stacks, scroll modes, overview |
| [Configuration](https://github.com/dip-bash/ananta/wiki/Configuration) | the full `config.toml` reference |
| [Status Bar](https://github.com/dip-bash/ananta/wiki/Status-Bar) | built-in + custom shell widgets |
| [Scrollback](https://github.com/dip-bash/ananta/wiki/Scrollback) | history, wheel, anchoring |
| [Copy Mode](https://github.com/dip-bash/ananta/wiki/Copy-Mode) | vim-style selection across all history |
| [Mouse and Selection](https://github.com/dip-bash/ananta/wiki/Mouse-and-Selection) | click / drag / wheel behavior |
| [Copy and Paste](https://github.com/dip-bash/ananta/wiki/Copy-and-Paste) | OSC 52 + native clipboard tools |
| [Architecture](https://github.com/dip-bash/ananta/wiki/Architecture) | package map, wire protocol, hacking guide |
| [FAQ](https://github.com/dip-bash/ananta/wiki/FAQ) / [Troubleshooting](https://github.com/dip-bash/ananta/wiki/Troubleshooting) | common questions and fixes |
| [Roadmap and Limitations](https://github.com/dip-bash/ananta/wiki/Roadmap-and-Limitations) | known gaps, planned work |

## Status

Linux-first (macOS builds; the system-stat bar widgets hide where `/proc`
doesn't exist). Not included yet: wide (CJK) glyphs, re-wrapped history on
resize, multiple attached clients per session - see the
[roadmap](https://github.com/dip-bash/ananta/wiki/Roadmap-and-Limitations).

## Contributing

`make test && make vet` before sending a patch. A guided tour of the source
lives in [Architecture](https://github.com/dip-bash/ananta/wiki/Architecture):
new actions go in `internal/config/actions.go` + `internal/mux/actions.go`,
new bar widgets in `internal/mux/bar.go`, key parsing in
`internal/input/keys.go`.
