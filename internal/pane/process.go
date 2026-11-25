package pane

import (
	"path/filepath"
	"strings"
	"time"
)

// ForegroundName returns the program currently in the foreground of the pane:
// the shell when the pane is idle, or the running tool (nvim, htop, ...).
// It is cached briefly because the vault asks for it repeatedly.
func (p *Pane) ForegroundName() string {
	if p == nil {
		return ""
	}
	now := time.Now()
	if p.fgName != "" && now.Sub(p.fgAt) < 500*time.Millisecond {
		return p.fgName
	}
	name := ""
	if p.PTY != nil {
		if pgrp, err := foregroundPgrp(p.PTY); err == nil && pgrp > 0 {
			name = processName(pgrp)
		}
	}
	if name == "" {
		name = p.Name
	}
	p.fgName, p.fgAt = name, now
	return name
}

// IsShell reports whether the pane is sitting at an interactive shell prompt.
func (p *Pane) IsShell() bool {
	return IsShellName(p.ForegroundName())
}

// IsShellName reports whether name looks like a command shell.
func IsShellName(name string) bool {
	name = strings.TrimPrefix(filepath.Base(strings.TrimSpace(name)), "-")
	switch name {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "mksh", "ash", "tcsh", "csh",
		"nu", "elvish", "xonsh", "oil", "osh", "busybox":
		return true
	}
	return false
}
