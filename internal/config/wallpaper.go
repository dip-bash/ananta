package config

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultWallpaper is the built-in wallpaper file (also written by `ananta init`).
//
//go:embed default_wallpaper.toml
var DefaultWallpaper string

// DefaultWallpaperFile is the file name that wallpaper = "default" refers to.
const DefaultWallpaperFile = "wallpaper.toml"

// WallpaperSpec describes the program that runs behind the panes.
type WallpaperSpec struct {
	Command string `toml:"command"`
	Dir     string `toml:"dir"`
	Dim     bool   `toml:"dim"`
	Restart bool   `toml:"restart"`
	Source  string `toml:"-"` // where it was loaded from
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// loadWallpaper resolves cfg.Wallpaper to a spec. Wallpaper files live in dir,
// the folder containing the main config file.
func (cfg *Config) loadWallpaper(dir string) {
	name := strings.TrimSpace(cfg.Wallpaper)
	switch name {
	case "", "none", "off":
		return
	}
	if name != filepath.Base(name) {
		cfg.warn("wallpaper must be a file name in %s, not a path", dir)
		return
	}

	spec := WallpaperSpec{Dim: true, Restart: true}
	var err error
	switch {
	case name == "default" && !exists(filepath.Join(dir, DefaultWallpaperFile)):
		spec.Source = "built-in default"
		_, err = toml.Decode(DefaultWallpaper, &spec)
	default:
		file := name
		if name == "default" {
			file = DefaultWallpaperFile
		}
		path := filepath.Join(dir, file)
		if !exists(path) && filepath.Ext(file) == "" {
			path += ".toml"
		}
		if !exists(path) {
			cfg.warn("wallpaper file not found: %s", path)
			return
		}
		spec.Source = path
		_, err = toml.DecodeFile(path, &spec)
	}
	if err != nil {
		cfg.warn("wallpaper %s: %v", spec.Source, err)
		return
	}
	if strings.TrimSpace(spec.Command) == "" {
		cfg.warn("wallpaper %s has no command", spec.Source)
		return
	}
	if strings.HasPrefix(spec.Dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			spec.Dir = filepath.Join(home, strings.TrimPrefix(spec.Dir, "~"))
		}
	}
	cfg.WallpaperSpec = &spec
}
