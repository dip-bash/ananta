# scrollmux

A scrollable tiling terminal multiplexer in Go (niri/PaperWM-style).

Panes live in columns on an endless horizontal strip. Focusing a column
glides the viewport to it; the strip never squeezes panes to fit.

## Build

    go mod tidy
    go build -o scrollmux .
    ./scrollmux --init      # writes ~/.config/scrollmux/config.toml
    ./scrollmux

## Default keys (all rebindable)

| Key | Action |
|---|---|
| alt+h/j/k/l or alt+arrows | focus left / down / up / right |
| alt+H/J/K/L | move column / pane |
| alt+enter | new column to the right |
| alt+s | stack a new pane below |
| alt+w | close pane |
| alt+, / alt+. | stack into left column / pop out to own column |
| alt+r | cycle width presets |
| alt+f | maximize toggle |
| alt+- / alt+= | shrink / grow column |
| alt+1..9 | go to workspace (alt+! alt+@ alt+# move pane to 1-3) |
| alt+/ | show all shortcuts |
| alt+Q | quit |

## Config

See `./scrollmux --print-config`. Highlights: `scroll_mode` ("fit"/"center"),
`animation_speed`, `border_style` (single/double/heavy/ascii, never rounded),
theme colors, and `[keys]`. Bind a key to a command with
`"alt+g" = "new_pane lazygit"`. Optional tmux-style `prefix` + `[prefix_keys]`.

## Not included yet

Per-pane scrollback, mouse support, detach/reattach sessions, wide (CJK) glyphs.
