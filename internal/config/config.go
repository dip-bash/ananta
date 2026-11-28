// Package config loads ~/.config/tst/config.toml on top of the embedded
// defaults and compiles key bindings into lookup tables.
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"tst/internal/input"
)

//go:embed default.toml
var Default string

type Theme struct {
	Border       string `toml:"border"`
	FocusBorder  string `toml:"focus_border"`
	MarkedBorder string `toml:"marked_border"`
	Title        string `toml:"title"`
	FocusTitle   string `toml:"focus_title"`
	StatusBG     string `toml:"status_bg"`
	StatusFG     string `toml:"status_fg"`
	Accent       string `toml:"accent"`
	SelectionBG  string `toml:"selection_bg"`
	SelectionFG  string `toml:"selection_fg"`
}

// CustomWidget is a user-defined status-bar widget backed by a shell command.
type CustomWidget struct {
	Command  string  `toml:"command"`  // shows the first line of its output, refreshed every Interval
	Interval float64 `toml:"interval"` // seconds
	Text     string  `toml:"text"`     // a fixed label (used when there is no command)
	Prefix   string  `toml:"prefix"`
	Color    string  `toml:"color"`

	// Making a widget clickable: a shell command and / or an action to run
	// when it is clicked with the left or the right mouse button.
	OnClick            string `toml:"on_click"`
	OnClickAction      string `toml:"on_click_action"`
	OnRightClick       string `toml:"on_right_click"`
	OnRightClickAction string `toml:"on_right_click_action"`
}

// Clickable reports whether clicking the widget does anything.
func (w CustomWidget) Clickable() bool {
	return w.OnClick != "" || w.OnClickAction != "" || w.OnRightClick != "" || w.OnRightClickAction != ""
}

type Bar struct {
	Position   string                  `toml:"position"`
	Interval   float64                 `toml:"interval"`
	Separator  string                  `toml:"separator"`
	Left       []string                `toml:"left"`
	Center     []string                `toml:"center"`
	Right      []string                `toml:"right"`
	TimeFormat string                  `toml:"time_format"`
	DateFormat string                  `toml:"date_format"`
	MemStyle   string                  `toml:"mem_style"`
	Widgets    map[string]CustomWidget `toml:"widgets"`
}

// BuiltinWidgets are the widget names the status bar understands natively.
var BuiltinWidgets = map[string]bool{
	"session": true, "workspaces": true, "columns": true, "mode": true, "message": true, "title": true, "help": true,
	"cpu": true, "mem": true, "net": true, "battery": true, "load": true,
	"time": true, "date": true, "host": true, "user": true,
}

type Config struct {
	Leader             string            `toml:"leader"`
	ExtendedKeys       string            `toml:"extended_keys"`
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
	ScrollbackLines    int               `toml:"scrollback_lines"`
	AppClipboard       bool              `toml:"app_clipboard"`
	Prefix             string            `toml:"prefix"`
	Wallpaper          string            `toml:"wallpaper"`
	Bar                Bar               `toml:"bar"`
	Theme              Theme             `toml:"theme"`
	Keys               map[string]string `toml:"keys"`
	PrefixKeys         map[string]string `toml:"prefix_keys"`

	// the built-in bindings, so a user binding of the same key overrides them
	defKeys, defPrefixKeys map[string]string

	// compiled
	LeaderMask int
	Keymap     map[string]Action
	PrefixMap  map[string]Action
	PrefixKey  string
	Warnings   []string

	// WallpaperSpec is the resolved wallpaper (nil when disabled).
	WallpaperSpec *WallpaperSpec
}

// Dir returns the config directory: $XDG_CONFIG_HOME/tst or ~/.config/tst.
func Dir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tst")
}

// Path returns the config file to use (flagPath wins when set).
func Path(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if d := Dir(); d != "" {
		return filepath.Join(d, "config.toml")
	}
	return ""
}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	if _, err := toml.Decode(Default, cfg); err != nil {
		return nil, fmt.Errorf("built-in config: %w", err)
	}
	cfg.defKeys, cfg.defPrefixKeys = cloneMap(cfg.Keys), cloneMap(cfg.PrefixKeys)
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	cfg.compile()
	dir := Dir()
	if path != "" {
		dir = filepath.Dir(path) // wallpaper files sit next to the config file in use
	}
	cfg.loadWallpaper(dir)
	return cfg, nil
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (cfg *Config) warn(format string, a ...any) {
	cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(format, a...))
}

func (cfg *Config) compile() {
	cfg.LeaderMask = input.ParseMods(cfg.Leader)
	if cfg.LeaderMask == 0 {
		cfg.warn("bad leader %s, using alt", strconv.Quote(cfg.Leader))
		cfg.LeaderMask = input.ModAlt
	}
	cfg.Leader = input.MaskName(cfg.LeaderMask)

	cfg.Keymap, cfg.PrefixMap = map[string]Action{}, map[string]Action{}
	compile := func(src, defs map[string]string, dst map[string]Action) {
		// Deterministic order: built-in bindings first, then the user's, each
		// sorted, so a user binding always overrides a default for the same key.
		isDef := func(chord string) bool { v, ok := defs[chord]; return ok && v == src[chord] }
		chords := make([]string, 0, len(src))
		for chord := range src {
			chords = append(chords, chord)
		}
		sort.Slice(chords, func(i, j int) bool {
			if di, dj := isDef(chords[i]), isDef(chords[j]); di != dj {
				return di
			}
			return chords[i] < chords[j]
		})
		from := map[string]string{}
		for _, chord := range chords {
			spec := src[chord]
			name := input.NormalizeChord(chord, cfg.LeaderMask)
			if prev, dup := from[name]; dup && !isDef(chord) && !isDef(prev) {
				cfg.warn("%s and %s are the same key; %s wins", prev, chord, chord)
			}
			from[name] = chord
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
			if a.Name == "exec" && a.Arg == "" {
				cfg.warn("exec needs a command: %s", chord)
				continue
			}
			if ActionIndex(a.Name) == len(ActionInfo) {
				// not an action name: the whole value is a shell command,
				// e.g. "tst open 'tst vault' -w 25%"
				a = Action{Name: "exec", Arg: spec}
			}
			dst[name] = a
		}
	}
	compile(cfg.Keys, cfg.defKeys, cfg.Keymap)
	compile(cfg.PrefixKeys, cfg.defPrefixKeys, cfg.PrefixMap)
	if cfg.Prefix != "" {
		cfg.PrefixKey = input.NormalizeChord(cfg.Prefix, cfg.LeaderMask)
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
	if cfg.AnimationSpeed <= 0 {
		cfg.AnimationSpeed = 18 // a non-positive speed makes the easing stall or diverge
	}
	if cfg.ResizeStep <= 0 {
		cfg.ResizeStep = 0.05
	}
	if cfg.ScrollbackLines < 0 {
		cfg.ScrollbackLines = 0
	}
	if cfg.OverviewScale < 0.15 || cfg.OverviewScale > 0.6 {
		cfg.OverviewScale = 0.3
	}
	if cfg.ScrollMode != "center" {
		cfg.ScrollMode = "fit"
	}
	switch cfg.Bar.Position {
	case "top", "bottom", "hidden":
	default:
		cfg.warn("bar.position must be top, bottom or hidden")
		cfg.Bar.Position = "bottom"
	}
	switch cfg.Bar.MemStyle {
	case "used", "percent", "both":
	default:
		cfg.warn("bar.mem_style must be used, percent or both (got %q)", cfg.Bar.MemStyle)
		cfg.Bar.MemStyle = "used"
	}
	if cfg.Bar.Interval < 0.2 {
		cfg.Bar.Interval = 1
	}
	for name, w := range cfg.Bar.Widgets {
		if w.Command == "" && w.Text == "" {
			cfg.warn("bar widget %s has neither command nor text", name)
		}
		for _, spec := range []string{w.OnClickAction, w.OnRightClickAction} {
			if act, _, _ := strings.Cut(strings.TrimSpace(spec), " "); act != "" && ActionIndex(act) == len(ActionInfo) {
				cfg.warn("bar widget %s: unknown action %s", name, act)
			}
		}
	}
	for _, list := range [][]string{cfg.Bar.Left, cfg.Bar.Center, cfg.Bar.Right} {
		for _, w := range list {
			if _, custom := cfg.Bar.Widgets[w]; !BuiltinWidgets[w] && !custom {
				cfg.warn("unknown bar widget: %s", w)
			}
		}
	}
}

// UseExtendedKeys reports whether the extended (CSI-u) keyboard protocol
// should be requested from the terminal.
func (c *Config) UseExtendedKeys() bool {
	switch c.ExtendedKeys {
	case "on", "true":
		return true
	case "off", "false":
		return false
	}
	if c.LeaderMask&(input.ModSuper|input.ModCtrl) != 0 {
		return true
	}
	for k := range c.Keymap {
		if strings.Contains(k, "super+") {
			return true
		}
	}
	return false
}

// decodeInto parses TOML text over cfg (used by tests).
func decodeInto(cfg *Config, text string) error {
	_, err := toml.Decode(text, cfg)
	if err == nil {
		cfg.compile()
	}
	return err
}

// TermModes returns the escape sequences that put the user's terminal into
// the state tst needs (alt screen, mouse, extended keyboard) and undo it.
func (c *Config) TermModes() (setup, teardown string) {
	setup = "\x1b[?1049h\x1b[?25l"
	teardown = "\x1b[0m\x1b[0 q\x1b[?25h\x1b[?1049l"
	if c.Mouse {
		setup += "\x1b[?1002h\x1b[?1006h"
		teardown = "\x1b[?1006l\x1b[?1002l" + teardown
	}
	if c.UseExtendedKeys() {
		setup += "\x1b[>5u"
		teardown = "\x1b[<u" + teardown
	}
	return
}
