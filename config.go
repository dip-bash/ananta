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
# shortcut, bind it to "none":   "leader+w" = "none"

# The modifier used by every "leader+..." shortcut below.
#   "alt" (default) | "ctrl" | "super" | "ctrl+alt" | ...
# "super" and "ctrl" need a terminal that speaks the kitty keyboard protocol
# (kitty, foot, wezterm, ghostty, alacritty >= 0.13, iTerm2, ...). Plain
# terminals can't send super, and ctrl+h/i/j/m are the same bytes as
# backspace/tab/enter, so "alt" is the safe choice there.
leader = "alt"
kitty_keyboard = "auto"    # "auto" (on for super/ctrl leaders) | "on" | "off"

shell = ""                 # empty = $SHELL
mouse = true               # click to focus, wheel, click in overview
animate = true             # smooth scrolling / zoom / column resize
animation_speed = 18.0     # higher = snappier
fps = 60
scroll_mode = "fit"        # "fit": PaperWM-style, scroll just enough to show the focused column
                           # "center": always keep the focused column centered
                           # (toggle at runtime with the center_mode action)
overview_scale = 0.3       # how far the overview zooms out (0.15 - 0.6)
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

# Actions (the in-app help lists whatever is bound here):
#   focus_left focus_right focus_up focus_down
#   move_left move_right move_up move_down
#   new_pane [command]        new column to the right of the focused one
#   new_pane_below [command]  stack a new pane under the focused one
#   close_pane stack_left unstack
#   grow shrink cycle_width maximize
#   center_column             center the focused column once
#   center_mode               toggle always-centered <-> PaperWM-style fit
#   first_column last_column
#   overview                  bird's-eye view of every workspace
#   workspace_next workspace_prev goto_workspace N move_to_workspace N
#   help quit
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
"leader+c" = "center_mode"
"leader+C" = "center_column"
"leader+o" = "overview"
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
"leader+/" = "help"
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

type Config struct {
	Leader             string            `toml:"leader"`
	KittyKeyboard      string            `toml:"kitty_keyboard"`
	Shell              string            `toml:"shell"`
	Mouse              bool              `toml:"mouse"`
	Animate            bool              `toml:"animate"`
	AnimationSpeed     float64           `toml:"animation_speed"`
	FPS                int               `toml:"fps"`
	ScrollMode         string            `toml:"scroll_mode"`
	OverviewScale      float64           `toml:"overview_scale"`
	BorderStyle        string            `toml:"border_style"`
	ColumnWidths       []float64         `toml:"column_widths"`
	DefaultColumnWidth float64           `toml:"default_column_width"`
	ResizeStep         float64           `toml:"resize_step"`
	QuitOnLastExit     bool              `toml:"quit_on_last_exit"`
	Prefix             string            `toml:"prefix"`
	Theme              Theme             `toml:"theme"`
	Keys               map[string]string `toml:"keys"`
	PrefixKeys         map[string]string `toml:"prefix_keys"`

	leaderMask int
	keymap     map[string]Action
	prefixmap  map[string]Action
	prefixKey  string
	warnings   []string
}

// actionInfo is the single source of truth for action names and the
// descriptions shown in the help screen (in this order).
var actionInfo = []struct{ name, desc string }{
	{"focus_left", "Focus column left"},
	{"focus_right", "Focus column right"},
	{"focus_up", "Focus pane above"},
	{"focus_down", "Focus pane below"},
	{"move_left", "Move column left"},
	{"move_right", "Move column right"},
	{"move_up", "Move pane up"},
	{"move_down", "Move pane down"},
	{"new_pane", "New column"},
	{"new_pane_below", "New pane below"},
	{"close_pane", "Close pane"},
	{"stack_left", "Stack into left column"},
	{"unstack", "Pop out into own column"},
	{"cycle_width", "Cycle column width"},
	{"maximize", "Maximize / restore column"},
	{"grow", "Widen column"},
	{"shrink", "Narrow column"},
	{"center_mode", "Toggle always-centered / PaperWM scroll"},
	{"center_column", "Center focused column"},
	{"first_column", "First column"},
	{"last_column", "Last column"},
	{"overview", "Toggle overview"},
	{"workspace_next", "Next workspace"},
	{"workspace_prev", "Previous workspace"},
	{"goto_workspace", "Go to workspace"},
	{"move_to_workspace", "Move pane to workspace"},
	{"help", "Toggle this help"},
	{"quit", "Quit"},
}

func actionIndex(name string) int {
	for i, a := range actionInfo {
		if a.name == name {
			return i
		}
	}
	return len(actionInfo)
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
	cfg.leaderMask = parseMods(cfg.Leader)
	if cfg.leaderMask == 0 {
		cfg.warnings = append(cfg.warnings, "bad leader "+strconv.Quote(cfg.Leader)+", using alt")
		cfg.leaderMask = modAlt
	}
	cfg.Leader = maskName(cfg.leaderMask)

	cfg.keymap, cfg.prefixmap = map[string]Action{}, map[string]Action{}
	compile := func(src map[string]string, dst map[string]Action) {
		for chord, spec := range src {
			name := normalizeChord(chord, cfg.leaderMask)
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
			if actionIndex(a.Name) == len(actionInfo) {
				cfg.warnings = append(cfg.warnings, "unknown action: "+a.Name)
				continue
			}
			dst[name] = a
		}
	}
	compile(cfg.Keys, cfg.keymap)
	compile(cfg.PrefixKeys, cfg.prefixmap)
	if cfg.Prefix != "" {
		cfg.prefixKey = normalizeChord(cfg.Prefix, cfg.leaderMask)
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
	if cfg.OverviewScale < 0.15 || cfg.OverviewScale > 0.6 {
		cfg.OverviewScale = 0.3
	}
	if cfg.ScrollMode != "center" {
		cfg.ScrollMode = "fit"
	}
	return cfg, nil
}

// useKitty reports whether the kitty keyboard protocol should be requested.
func (c *Config) useKitty() bool {
	switch c.KittyKeyboard {
	case "on", "true":
		return true
	case "off", "false":
		return false
	}
	if c.leaderMask&(modSuper|modCtrl) != 0 {
		return true
	}
	for k := range c.keymap {
		if strings.Contains(k, "super+") {
			return true
		}
	}
	return false
}

// keyFor returns the first chord bound to an action ("" if none).
func (c *Config) keyFor(action string) string {
	best := ""
	for k, a := range c.keymap {
		if a.Name == action && a.Arg == "" && (best == "" || len(k) < len(best) || (len(k) == len(best) && k < best)) {
			best = k
		}
	}
	return best
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

// helpLines builds the help screen from the live keymap, one line per action,
// so it always reflects the user's config.
func (c *Config) helpLines() []string {
	type entry struct {
		name, arg string
		keys      []string
	}
	byspec := map[string]*entry{}
	add := func(key string, a Action) {
		spec := a.Name + " " + a.Arg
		e := byspec[spec]
		if e == nil {
			e = &entry{name: a.Name, arg: a.Arg}
			byspec[spec] = e
		}
		e.keys = append(e.keys, key)
	}
	for k, a := range c.keymap {
		add(k, a)
	}
	for k, a := range c.prefixmap {
		add(c.prefixKey+" "+k, a)
	}
	var es []*entry
	for _, e := range byspec {
		sort.Strings(e.keys)
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if ai, bi := actionIndex(a.name), actionIndex(b.name); ai != bi {
			return ai < bi
		}
		an, ae := strconv.Atoi(a.arg)
		bn, be := strconv.Atoi(b.arg)
		if ae == nil && be == nil {
			return an < bn
		}
		return a.arg < b.arg
	})
	kw := 0
	keystr := make([]string, len(es))
	for i, e := range es {
		keystr[i] = strings.Join(e.keys, ", ")
		kw = max(kw, len([]rune(keystr[i])))
	}
	kw = min(kw, 34)
	var out []string
	for i, e := range es {
		desc := actionInfo[min(actionIndex(e.name), len(actionInfo)-1)].desc
		if actionIndex(e.name) == len(actionInfo) {
			desc = e.name
		}
		if e.arg != "" {
			if e.name == "goto_workspace" || e.name == "move_to_workspace" {
				desc += " " + e.arg
			} else {
				desc += ": " + e.arg
			}
		}
		out = append(out, fmt.Sprintf("%-*s  %s", kw, truncate(keystr[i], kw), desc))
	}
	if c.Mouse {
		out = append(out, "", "mouse: click a pane to focus it (in the overview: focus + center)",
			"mouse: wheel = scroll full-screen apps / switch workspace in overview",
			"mouse: hold shift to select text with your terminal")
	}
	return out
}
