package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig makes a config dir with the given files and loads config.toml.
func writeConfig(t *testing.T, files map[string]string) *Config {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestWallpaperDefaultIsBuiltIn(t *testing.T) {
	cfg := writeConfig(t, map[string]string{"config.toml": ""})
	w := cfg.WallpaperSpec
	if w == nil || !strings.Contains(w.Command, "date") || !w.Dim || !w.Restart {
		t.Fatalf("built-in wallpaper not loaded: %+v (warnings %v)", w, cfg.Warnings)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings: %v", cfg.Warnings)
	}
}

func TestWallpaperDefaultFileInConfigFolderWins(t *testing.T) {
	cfg := writeConfig(t, map[string]string{
		"config.toml":    "",
		"wallpaper.toml": "command = \"htop\"\ndim = false\n",
	})
	w := cfg.WallpaperSpec
	if w == nil || w.Command != "htop" || w.Dim || !w.Restart {
		t.Fatalf("got %+v", w)
	}
}

func TestWallpaperNamedFile(t *testing.T) {
	for _, name := range []string{"matrix.toml", "matrix"} { // extension optional
		cfg := writeConfig(t, map[string]string{
			"config.toml": "wallpaper = \"" + name + "\"\n",
			"matrix.toml": "command = \"cmatrix\"\n",
		})
		if w := cfg.WallpaperSpec; w == nil || w.Command != "cmatrix" {
			t.Fatalf("%s: got %+v warnings %v", name, w, cfg.Warnings)
		}
	}
}

func TestWallpaperProblemsWarnAndDisable(t *testing.T) {
	cases := map[string]map[string]string{
		"wallpaper file not found": {"config.toml": "wallpaper = \"nope.toml\"\n"},
		"not a path":               {"config.toml": "wallpaper = \"../escape.toml\"\n"},
		"has no command":           {"config.toml": "wallpaper = \"x.toml\"\n", "x.toml": "dim = true\n"},
	}
	for want, files := range cases {
		cfg := writeConfig(t, files)
		if cfg.WallpaperSpec != nil {
			t.Errorf("%q: wallpaper should be disabled", want)
		}
		if len(cfg.Warnings) == 0 || !strings.Contains(strings.Join(cfg.Warnings, ";"), want) {
			t.Errorf("%q: warnings = %v", want, cfg.Warnings)
		}
	}
}

func TestWallpaperCanBeDisabled(t *testing.T) {
	for _, v := range []string{"none", "off", ""} {
		cfg := writeConfig(t, map[string]string{"config.toml": "wallpaper = \"" + v + "\"\n"})
		if cfg.WallpaperSpec != nil || len(cfg.Warnings) != 0 {
			t.Errorf("%q: spec %+v warnings %v", v, cfg.WallpaperSpec, cfg.Warnings)
		}
	}
}

func TestRemovedActionsAreGone(t *testing.T) {
	cfg := writeConfig(t, map[string]string{"config.toml": ""})
	for chord, a := range cfg.Keymap {
		if a.Name == "paste" || a.Name == "center_column" {
			t.Errorf("%s still bound to %s", chord, a.Name)
		}
	}
	for i, want := range []string{"$", "%", "^", "&", "*", "("} {
		got := cfg.Keymap["alt+"+want]
		if got.Name != "move_to_workspace" || got.Arg != string(rune('4'+i)) {
			t.Errorf("alt+%s -> %+v, want move_to_workspace %c", want, got, '4'+i)
		}
	}
}
