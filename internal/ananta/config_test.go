package ananta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandLeader(t *testing.T) {
	if got := normalizeChord(expandLeader("leader+h", "ctrl")); got != "ctrl+h" {
		t.Fatalf("expected ctrl+h, got %q", got)
	}
	if got := normalizeChord(expandLeader("alt+h", "ctrl")); got != "alt+h" {
		t.Fatalf("explicit binding changed: %q", got)
	}
}

func TestLoadConfigLeaderAndHelpBindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := `
leader = "ctrl"
prefix = "ctrl+b"
[keys]
"leader+x" = "help"
[prefix_keys]
"h" = "help"
`
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.keymap["ctrl+x"]; !ok {
		t.Fatalf("expected leader expansion to ctrl+x")
	}
	lines := cfg.helpLines()
	foundPrefix := false
	for _, l := range lines {
		if strings.Contains(l, "ctrl+b then h") && strings.Contains(l, "help") {
			foundPrefix = true
		}
	}
	if !foundPrefix {
		t.Fatalf("expected prefix help line in help output: %#v", lines)
	}
}

func TestLoadConfigInitialLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	data := `
initial_layout = [
  { columns = [{ command = "", width = 0.5 }, { command = "htop", width = 0.5 }] },
  { columns = [{ command = "vim", width = 1.0 }] },
]
`
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.InitialLayout) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(cfg.InitialLayout))
	}
	if len(cfg.InitialLayout[0].Columns) != 2 {
		t.Fatalf("expected 2 columns in workspace 1, got %d", len(cfg.InitialLayout[0].Columns))
	}
	if cfg.InitialLayout[0].Columns[0].Command != "" {
		t.Fatalf("expected empty command, got %q", cfg.InitialLayout[0].Columns[0].Command)
	}
	if cfg.InitialLayout[0].Columns[1].Command != "htop" {
		t.Fatalf("expected htop, got %q", cfg.InitialLayout[0].Columns[1].Command)
	}
	if cfg.InitialLayout[1].Columns[0].Command != "vim" {
		t.Fatalf("expected vim, got %q", cfg.InitialLayout[1].Columns[0].Command)
	}
	if cfg.InitialLayout[1].Columns[0].Width != 1.0 {
		t.Fatalf("expected width 1.0, got %f", cfg.InitialLayout[1].Columns[0].Width)
	}
}

func TestLoadConfigResizeStepValidation(t *testing.T) {
	tests := []struct {
		name           string
		resizeStep     float64
		expectedStep   float64
		expectWarning  bool
	}{
		{"valid", 0.05, 0.05, false},
		{"too large", 2.0, 0.05, true},
		{"negative", -0.1, 0.05, true},
		{"zero", 0, 0.05, true},
		{"one", 1.0, 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			data := fmt.Sprintf(`resize_step = %f`, tt.resizeStep)
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ResizeStep != tt.expectedStep {
				t.Fatalf("expected resize_step %f, got %f", tt.expectedStep, cfg.ResizeStep)
			}
			hasWarning := false
			for _, w := range cfg.Warnings() {
				if strings.Contains(w, "resize_step") {
					hasWarning = true
					break
				}
			}
			if hasWarning != tt.expectWarning {
				t.Fatalf("expected warning=%v, got %v (warnings: %v)", tt.expectWarning, hasWarning, cfg.Warnings())
			}
		})
	}
}
