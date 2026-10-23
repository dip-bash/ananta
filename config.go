package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const defaultConfig = `# scrollmux configuration  (~/.config/scrollmux/config.toml)
#
# Any key you set here is merged over the defaults. To remove a default
# shortcut, bind it to "none":   "alt+w" = "none"

shell = ""                 # empty = $SHELL
animate = true             # smooth scrolling / column resize
animation_speed = 18.0     # higher = snappier
fps = 60
scroll_mode = "fit"        # "fit": scroll just enough to show the focused column
                           # "center": keep the focused column centered
border_style = "single"    # single | double | heavy | ascii   (square corners only)
column_widths = [0.33, 0.5, 0.66, 1.0]   # presets used by cycle_width (fraction of screen)
default_column_width = 0.5
resize_step = 0.05         # grow / shrink step (fraction of screen)
quit_on_last_exit = true

# Optional tmux-style prefix. When set, keys in [prefix_keys] work after it.
prefix = ""                # e.g. "ctrl+b"

[theme]                    # "#rrggbb", 0-255 (palette) or "default"
border       = "#4c566a"
focus_border = "#88c0d0"
title        = "#81a1c1"
focus_title  = "#eceff4"
status_bg    = "#2e3440"
status_fg    = "#d8dee9"
accent       = "#88c0d0"

# Actions:
#   focus_left focus_right focus_up focus_down
#   move_left move_right move_up move_down
#   new_pane [command]        new column to the right of the focused one
#   new_pane_below [command]  stack a new pane under the focused one
#   close_pane
#   stack_left                move pane into the column on its left
#   unstack                   pop pane out into its own column
#   grow shrink cycle_width maximize
#   first_column last_column
#   workspace_next workspace_prev goto_workspace N move_to_workspace N
#   help quit
[keys]
"alt+h" = "focus_left"
"alt+left" = "focus_left"
"alt+l" = "focus_right"
"alt+right" = "focus_right"
"alt+j" = "focus_down"
"alt+down" = "focus_down"
"alt+k" = "focus_up"
"alt+up" = "focus_up"
"alt+H" = "move_left"
"alt+shift+left" = "move_left"
"alt+L" = "move_right"
"alt+shift+right" = "move_right"
"alt+J" = "move_down"
"alt+shift+down" = "move_down"
"alt+K" = "move_up"
"alt+shift+up" = "move_up"
"alt+enter" = "new_pane"
"alt+s" = "new_pane_below"
"alt+w" = "close_pane"
"alt+," = "stack_left"
"alt+." = "unstack"
"alt+r" = "cycle_width"
"alt+f" = "maximize"
"alt+-" = "shrink"
"alt+=" = "grow"
"alt+home" = "first_column"
"alt+end" = "last_column"
"alt+pagedown" = "workspace_next"
"alt+pageup" = "workspace_prev"
"alt+1" = "goto_workspace 1"
"alt+2" = "goto_workspace 2"
"alt+3" = "goto_workspace 3"
"alt+4" = "goto_workspace 4"
"alt+5" = "goto_workspace 5"
"alt+6" = "goto_workspace 6"
"alt+7" = "goto_workspace 7"
"alt+8" = "goto_workspace 8"
"alt+9" = "goto_workspace 9"
"alt+!" = "move_to_workspace 1"
"alt+@" = "move_to_workspace 2"
"alt+#" = "move_to_workspace 3"
"alt+/" = "help"
"alt+Q" = "quit"

[prefix_keys]
`

type Theme struct {
	Border      string `toml:"border"`
	FocusBorder string `toml:"focus_border"`
	Title       string `toml:"title"`
	FocusTitle  string `toml:"focus_title"`
	StatusBG    string `toml:"status_bg"`
	StatusFG    string `toml:"status_fg"`
	Accent      string `toml:"accent"`
}

type Action struct{ Name, Arg string }

type Config struct {
	Shell              string            `toml:"shell"`
	Animate            bool              `toml:"animate"`
	AnimationSpeed     float64           `toml:"animation_speed"`
	FPS                int               `toml:"fps"`
	ScrollMode         string            `toml:"scroll_mode"`
	BorderStyle        string            `toml:"border_style"`
	ColumnWidths       []float64         `toml:"column_widths"`
	DefaultColumnWidth float64           `toml:"default_column_width"`
	ResizeStep         float64           `toml:"resize_step"`
	QuitOnLastExit     bool              `toml:"quit_on_last_exit"`
	Prefix             string            `toml:"prefix"`
	Theme              Theme             `toml:"theme"`
	Keys               map[string]string `toml:"keys"`
	PrefixKeys         map[string]string `toml:"prefix_keys"`

	keymap    map[string]Action
	prefixmap map[string]Action
	prefixKey string
	warnings  []string
}

var knownActions = map[string]bool{
	"focus_left": true, "focus_right": true, "focus_up": true, "focus_down": true,
	"move_left": true, "move_right": true, "move_up": true, "move_down": true,
	"new_pane": true, "new_pane_below": true, "close_pane": true,
	"stack_left": true, "unstack": true,
	"grow": true, "shrink": true, "cycle_width": true, "maximize": true,
	"first_column": true, "last_column": true,
	"workspace_next": true, "workspace_prev": true, "goto_workspace": true, "move_to_workspace": true,
	"help": true, "quit": true,
}

func configPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "scrollmux", "config.toml")
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.Decode(defaultConfig, cfg); err != nil {
		return nil, fmt.Errorf("built-in config: %w", err)
	}
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	cfg.keymap, cfg.prefixmap = map[string]Action{}, map[string]Action{}
	compile := func(src map[string]string, dst map[string]Action) {
		for chord, spec := range src {
			name := normalizeChord(chord)
			spec = strings.TrimSpace(spec)
			if spec == "" || spec == "none" || spec == "unbind" {
				delete(dst, name)
				continue
			}
			parts := strings.SplitN(spec, " ", 2)
			a := Action{Name: parts[0]}
			if len(parts) == 2 {
				a.Arg = strings.TrimSpace(parts[1])
			}
			if !knownActions[a.Name] {
				cfg.warnings = append(cfg.warnings, "unknown action: "+a.Name)
				continue
			}
			dst[name] = a
		}
	}
	compile(cfg.Keys, cfg.keymap)
	compile(cfg.PrefixKeys, cfg.prefixmap)
	if cfg.Prefix != "" {
		cfg.prefixKey = normalizeChord(cfg.Prefix)
	}
	if cfg.FPS < 10 || cfg.FPS > 240 {
		cfg.FPS = 60
	}
	if len(cfg.ColumnWidths) == 0 {
		cfg.ColumnWidths = []float64{0.5, 1.0}
	}
	if cfg.DefaultColumnWidth <= 0 || cfg.DefaultColumnWidth > 1 {
		cfg.DefaultColumnWidth = 0.5
	}
	return cfg, nil
}

func parseColor(s string) Col {
	s = strings.TrimSpace(s)
	if s == "" || s == "default" {
		return ColDefault
	}
	if strings.HasPrefix(s, "#") && len(s) == 7 {
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err == nil {
			return RGB(uint8(v>>16), uint8(v>>8), uint8(v))
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < 256 {
		return Col(n)
	}
	return ColDefault
}

type borderSet struct{ h, v, tl, tr, bl, br rune }

func borderFor(style string) borderSet {
	switch style {
	case "double":
		return borderSet{'═', '║', '╔', '╗', '╚', '╝'}
	case "heavy":
		return borderSet{'━', '┃', '┏', '┓', '┗', '┛'}
	case "ascii":
		return borderSet{'-', '|', '+', '+', '+', '+'}
	}
	return borderSet{'─', '│', '┌', '┐', '└', '┘'} // never rounded
}

// helpLines lists the active shortcuts, grouped by action.
func (c *Config) helpLines() []string {
	type kv struct{ key, act string }
	var all []kv
	for k, a := range c.keymap {
		s := a.Name
		if a.Arg != "" {
			s += " " + a.Arg
		}
		all = append(all, kv{k, s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].act != all[j].act {
			return all[i].act < all[j].act
		}
		return all[i].key < all[j].key
	})
	var out []string
	for _, e := range all {
		out = append(out, fmt.Sprintf("%-18s %s", e.key, e.act))
	}
	return out
}
