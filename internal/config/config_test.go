package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultsLoadCleanly(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("default config has warnings: %v", cfg.Warnings)
	}
	if a := cfg.Keymap["alt+h"]; a.Name != "focus_left" {
		t.Fatalf("alt+h bound to %+v", a)
	}
	if a := cfg.Keymap["alt+a"]; a.Name != "copy_mode" {
		t.Fatalf("alt+a bound to %+v", a)
	}
	if len(cfg.HelpLines()) < 40 {
		t.Fatal("help looks too short")
	}
}

func TestLeaderChange(t *testing.T) {
	cfg := &Config{}
	if err := decodeInto(cfg, Default+"\n"); err != nil {
		t.Fatal(err)
	}
	cfg.Leader = "super"
	cfg.compile()
	if cfg.Keymap["super+h"].Name != "focus_left" {
		t.Fatal("leader=super did not rebind leader+h")
	}
	if !cfg.UseExtendedKeys() {
		t.Fatal("super leader should enable the extended keyboard protocol")
	}
}
