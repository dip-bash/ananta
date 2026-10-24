# scrollmux

A scrollable tiling terminal multiplexer in Go (niri / PaperWM style).
Panes live in columns on an endless horizontal strip; focusing a column glides
the viewport to it. Square borders, animated scrolling/zoom, mouse support.

## Build

    go mod tidy
    go build -o scrollmux .
    ./scrollmux --init      # writes ~/.config/scrollmux/config.toml
    ./scrollmux

## Leader key

Every default shortcut is written as `leader+<key>` in the config. Change one
line to move them all:

    leader = "alt"        # or "super", "ctrl", "ctrl+alt", ...

`super` and `ctrl` need a terminal with the kitty keyboard protocol (kitty,
foot, wezterm, ghostty, alacritty >= 0.13, ...). Plain terminals cannot send
super, and ctrl+h/i/j/m collide with backspace/tab/enter, so `alt` is the safe
default. You can still bind single chords explicitly (`"super+t" = "new_pane"`).

## Default keys (leader = alt)

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
| leader+o | overview (bird's-eye view of all workspaces) |
| leader+1..9 | workspace (leader+! @ # move pane to 1-3) |
| leader+/ | help (scrollable, generated from your config) |
| leader+Q | quit |

In the overview: h/j/k/l or arrows move focus, Enter centers and exits, Esc exits.
The help screen scrolls with j/k, PgUp/PgDn, g/G or the mouse wheel.

## Mouse

Click a pane to focus it. In the overview, clicking a window focuses it, zooms
back in and centers it. Wheel switches workspaces in the overview and scrolls
full-screen apps (less, vim) otherwise; apps that request mouse reporting
receive the events. Hold shift to select text with your terminal.
Disable with `mouse = false`.

## Config

`./scrollmux --print-config` shows every option: `scroll_mode`,
`overview_scale`, `animation_speed`, `border_style` (single/double/heavy/ascii,
never rounded), theme colors, `[keys]`, optional tmux-style `prefix`.
Bind a key to a command: `"leader+g" = "new_pane lazygit"`.
Unbind a default: `"leader+w" = "none"`.

## Not included yet

Per-pane scrollback, detach/reattach sessions, wide (CJK) glyphs.
