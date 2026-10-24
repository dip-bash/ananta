# ananta

A scrollable tiling terminal multiplexer in Go (niri/PaperWM-style).

## Project structure

```text
cmd/ananta/            CLI entrypoint
internal/ananta/       app runtime: config, input parsing, layout, PTY panes, rendering, status panel
```

## Build and run

```bash
go mod tidy
go build -o ananta ./cmd/ananta
./ananta --init      # writes ~/.config/ananta/config.toml
./ananta
```

`ananta` reads `~/.config/ananta/config.toml` by default. If that file does not exist but `~/.config/scrollmux/config.toml` exists, it is used as a compatibility fallback.

## Default keys (all rebindable)

Defaults use `leader = "alt"` and keys are defined as `leader+...`.

| Key | Action |
|---|---|
| leader+h/j/k/l or leader+arrows | focus left / down / up / right |
| leader+H/J/K/L | move column / pane |
| leader+enter | new column to the right |
| leader+s | stack a new pane below |
| leader+w | close pane |
| leader+, / leader+. | stack into left column / pop out to own column |
| leader+r | cycle width presets |
| leader+f | maximize toggle |
| leader+- / leader+= | shrink / grow column |
| leader+o | toggle overview mode |
| leader+1..9 | go to workspace (creates if next in sequence, like i3wm) |
| leader+! leader+@ leader+# | move pane to workspace 1-3 |
| leader+? | show shortcuts |
| esc (while help open) | close help |
| leader+Q | quit |

## Workspaces

Workspaces are created dynamically like i3wm:
- Start with workspace 1 only
- `leader+2` creates workspace 2 (if only workspace 1 exists)
- `leader+3` only works after workspace 2 exists
- `leader+N` creates workspace N if N == current_count + 1
- `leader+o` (overview) shows all existing workspaces

## Status panel

Panel location is configurable with:

```toml
[status]
position = "bottom" # top | bottom
```

The panel is fully configurable in left/center/right sections:

```toml
[status]
left = ["workspaces", "panes"]
center = ["cwd"]
right = ["cpu", "memory", "load", "battery", "time"]
```

Supported items: `workspaces`, `panes`, `cwd`, `battery`, `cpu`, `memory`, `load`, `time`.

Unknown item names are ignored with a config warning. Battery/resource items degrade gracefully if unavailable.

Mouse interactions:
- click workspace indicator: switch workspace
- click pane/column indicator: focus that column
- click pane body: focus that pane

## Initial Layout

Define startup panes per workspace in config:

```toml
initial_layout = [
  { columns = [{ command = "", width = 0.5 }, { command = "htop", width = 0.5 }] },  # workspace 1: 2 columns
  { columns = [{ command = "vim", width = 1.0 }] },                                   # workspace 2: 1 column (created on first access)
]
```

- Workspaces are created lazily when first accessed
- Each workspace can have multiple columns with optional commands
- `width` is fraction of screen (uses `default_column_width` if 0 or omitted)

## Notes

- Help overlay is scrollable and resolves active keybindings from your current config.
- `super` is accepted in config, but works only if your terminal emits a distinct Super-key sequence.
