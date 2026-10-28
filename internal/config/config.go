// Package config loads ~/.config/ananta/config.toml on top of the embedded
// defaults and compiles key bindings into lookup tables.
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"ananta/internal/input"
)

//go:embed default.toml
var Default string

type Theme struct {
	Border      string `toml:"border"`
	FocusBorder string `toml:"focus_border"`
	Title       string `toml:"title"`
	FocusTitle  string `toml:"focus_title"`
	StatusBG    string `toml:"status_bg"`
	StatusFG    string `toml:"status_fg"`
	Accent      string `toml:"accent"`
	SelectionBG string `toml:"selection_bg"`
	SelectionFG string `toml:"selection_fg"`
}

// CustomWidget is a user-defined status-bar widget backed by a shell command.
type CustomWidget struct {
	Command  string  `toml:"command"`
	Interval float64 `toml:"interval"`
	Prefix   string  `toml:"prefix"`
	Color    string  `toml:"color"`
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

	// compiled
	LeaderMask int
	Keymap     map[string]Action
	PrefixMap  map[string]Action
	PrefixKey  string
	Warnings   []string

	// WallpaperSpec is the resolved wallpaper (nil when disabled).
	WallpaperSpec *WallpaperSpec
}

// Dir returns the config directory: $XDG_CONFIG_HOME/ananta or ~/.config/ananta.
func Dir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ananta")
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
	compile := func(src map[string]string, dst map[string]Action) {
		for chord, spec := range src {
			name := input.NormalizeChord(chord, cfg.LeaderMask)
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
			if ActionIndex(a.Name) == len(ActionInfo) {
				cfg.warn("unknown action: %s", a.Name)
				continue
			}
			dst[name] = a
		}
	}
	compile(cfg.Keys, cfg.Keymap)
	compile(cfg.PrefixKeys, cfg.PrefixMap)
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
// the state ananta needs (alt screen, mouse, extended keyboard) and undo it.
func (c *Config) TermModes() (setup, teardown string) {
	setup = "\x1b[?1049h\x1b[?25l"
	teardown = "\x1b[0m\x1b[?25h\x1b[?1049l"
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
