package ananta

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const defaultConfig = `# ananta configuration  (~/.config/ananta/config.toml)
#
# Any key you set here is merged over the defaults. To remove a default
# shortcut, bind it to "none":   "alt+w" = "none"

shell = ""                 # empty = $SHELL
animate = true             # smooth scrolling / column resize
animation_speed = 18.0     # higher = snappier
fps = 60
scroll_mode = "fit"        # "fit": scroll just enough to show the focused column
                           # "center": keep the focused column centered
leader = "alt"             # leader token used in keybindings: alt | ctrl | shift | super
                           # "super" depends on your terminal emitting a distinct Super key sequence
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

[status]
position = "bottom" # top | bottom
left = ["workspaces", "panes"]
center = ["cwd"]
right = ["cpu", "memory", "load", "battery", "time"]

# Initial layout per workspace (1-9). Workspaces are created lazily when first accessed.
# If empty, a single pane is created in workspace 1.
# [[initial_layout.workspaces]]
#   [[initial_layout.workspaces.columns]]
#     command = ""        # optional command to run in this column
#     width = 0.5         # fraction of screen (uses default_column_width if 0)
# initial_layout = [
#   { columns = [{ command = "", width = 0.5 }, { command = "htop", width = 0.5 }] },  # workspace 1: 2 columns
#   { columns = [{ command = "vim", width = 1.0 }] },                                   # workspace 2: 1 column
# ]

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
#   overview help quit
[keys]
"leader+h" = "focus_left"
"leader+left" = "focus_left"
"leader+l" = "focus_right"
"leader+right" = "focus_right"
"leader+j" = "focus_down"
"leader+down" = "focus_down"
"leader+k" = "focus_up"
"leader+up" = "focus_up"
"leader+H" = "move_left"
"leader+shift+left" = "move_left"
"leader+L" = "move_right"
"leader+shift+right" = "move_right"
"leader+J" = "move_down"
"leader+shift+down" = "move_down"
"leader+K" = "move_up"
"leader+shift+up" = "move_up"
"leader+enter" = "new_pane"
"leader+s" = "new_pane_below"
"leader+w" = "close_pane"
"leader+," = "stack_left"
"leader+." = "unstack"
"leader+r" = "cycle_width"
"leader+f" = "maximize"
"leader+-" = "shrink"
"leader+=" = "grow"
"leader+home" = "first_column"
"leader+end" = "last_column"
"leader+pagedown" = "workspace_next"
"leader+pageup" = "workspace_prev"
"leader+1" = "goto_workspace 1"
"leader+2" = "goto_workspace 2"
"leader+3" = "goto_workspace 3"
"leader+4" = "goto_workspace 4"
"leader+5" = "goto_workspace 5"
"leader+6" = "goto_workspace 6"
"leader+7" = "goto_workspace 7"
"leader+8" = "goto_workspace 8"
"leader+9" = "goto_workspace 9"
"leader+!" = "move_to_workspace 1"
"leader+@" = "move_to_workspace 2"
"leader+#" = "move_to_workspace 3"
"leader+o" = "overview"
"leader+?" = "help"
"leader+Q" = "quit"

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

type StatusConfig struct {
	Position string   `toml:"position"`
	Left     []string `toml:"left"`
	Center   []string `toml:"center"`
	Right    []string `toml:"right"`
}

type ColumnLayout struct {
	Command string  `toml:"command"`
	Width   float64 `toml:"width"`
}

type WorkspaceLayout struct {
	Columns []ColumnLayout `toml:"columns"`
}

type Config struct {
	Shell              string            `toml:"shell"`
	Animate            bool              `toml:"animate"`
	AnimationSpeed     float64           `toml:"animation_speed"`
	FPS                int               `toml:"fps"`
	ScrollMode         string            `toml:"scroll_mode"`
	Leader             string            `toml:"leader"`
	BorderStyle        string            `toml:"border_style"`
	ColumnWidths       []float64         `toml:"column_widths"`
	DefaultColumnWidth float64           `toml:"default_column_width"`
	ResizeStep         float64           `toml:"resize_step"`
	QuitOnLastExit     bool              `toml:"quit_on_last_exit"`
	Prefix             string            `toml:"prefix"`
	Theme              Theme             `toml:"theme"`
	Status             StatusConfig      `toml:"status"`
	Keys               map[string]string `toml:"keys"`
	PrefixKeys         map[string]string `toml:"prefix_keys"`
	InitialLayout      []WorkspaceLayout `toml:"initial_layout"`

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
	"overview": true, "help": true, "quit": true,
}

func configPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	newPath := filepath.Join(dir, "ananta", "config.toml")
	oldPath := filepath.Join(dir, "scrollmux", "config.toml")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if _, err := os.Stat(oldPath); err == nil {
		return oldPath
	}
	return newPath
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
	cfg.Leader = normalizeLeader(cfg.Leader)
	compile := func(src map[string]string, dst map[string]Action) {
		for chord, spec := range src {
			name := normalizeChord(expandLeader(chord, cfg.Leader))
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
	if cfg.ResizeStep <= 0 || cfg.ResizeStep > 1 {
		cfg.warnings = append(cfg.warnings, "invalid resize_step: "+fmt.Sprintf("%g", cfg.ResizeStep)+" (using 0.05)")
		cfg.ResizeStep = 0.05
	}
	if cfg.ScrollMode != "fit" && cfg.ScrollMode != "center" {
		cfg.warnings = append(cfg.warnings, "invalid scroll_mode: "+cfg.ScrollMode+" (using fit)")
		cfg.ScrollMode = "fit"
	}
	cfg.Status.Position = strings.ToLower(strings.TrimSpace(cfg.Status.Position))
	switch cfg.Status.Position {
	case "", "bottom":
		cfg.Status.Position = "bottom"
	case "top":
	default:
		cfg.warnings = append(cfg.warnings, "invalid status.position: "+cfg.Status.Position+" (using bottom)")
		cfg.Status.Position = "bottom"
	}
	if len(cfg.Status.Left) == 0 && len(cfg.Status.Center) == 0 && len(cfg.Status.Right) == 0 {
		cfg.Status.Left = []string{"workspaces", "panes"}
		cfg.Status.Center = []string{"cwd"}
		cfg.Status.Right = []string{"cpu", "memory", "load", "battery", "time"}
	}
	cfg.Status.Left = cfg.validateStatusItems("left", cfg.Status.Left)
	cfg.Status.Center = cfg.validateStatusItems("center", cfg.Status.Center)
	cfg.Status.Right = cfg.validateStatusItems("right", cfg.Status.Right)
	return cfg, nil
}

var knownStatusItems = map[string]bool{
	"workspaces": true,
	"panes":      true,
	"cwd":        true,
	"battery":    true,
	"cpu":        true,
	"memory":     true,
	"load":       true,
	"time":       true,
}

func (c *Config) validateStatusItems(section string, in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		if !knownStatusItems[name] {
			c.warnings = append(c.warnings, fmt.Sprintf("unknown status item in %s: %s", section, item))
			continue
		}
		out = append(out, name)
	}
	return out
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
	if c.prefixKey != "" {
		var pref []kv
		for k, a := range c.prefixmap {
			s := a.Name
			if a.Arg != "" {
				s += " " + a.Arg
			}
			pref = append(pref, kv{fmt.Sprintf("%s then %s", c.prefixKey, k), s})
		}
		sort.Slice(pref, func(i, j int) bool {
			if pref[i].act != pref[j].act {
				return pref[i].act < pref[j].act
			}
			return pref[i].key < pref[j].key
		})
		for _, e := range pref {
			out = append(out, fmt.Sprintf("%-18s %s", e.key, e.act))
		}
	}
	return out
}

func normalizeLeader(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ctrl", "control":
		return "ctrl"
	case "shift":
		return "shift"
	case "super", "win", "windows", "cmd", "command":
		return "super"
	default:
		return "alt"
	}
}

func expandLeader(chord, leader string) string {
	parts := strings.Split(chord, "+")
	if len(parts) == 0 {
		return chord
	}
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(strings.TrimSpace(parts[i]), "leader") {
			parts[i] = leader
		}
	}
	return strings.Join(parts, "+")
}

func (c *Config) bindingsForAction(action string) []string {
	var out []string
	for k, a := range c.keymap {
		if a.Name == action {
			out = append(out, k)
		}
	}
	for k, a := range c.prefixmap {
		if a.Name == action && c.prefixKey != "" {
			out = append(out, fmt.Sprintf("%s then %s", c.prefixKey, k))
		}
	}
	sort.Strings(out)
	return out
}

func (c *Config) primaryBinding(action, fallback string) string {
	b := c.bindingsForAction(action)
	if len(b) == 0 {
		return fallback
	}
	return b[0]
}

func (c *Config) Warnings() []string {
	return c.warnings
}
