//go:build linux || darwin

package pane

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// foregroundPgrp asks the terminal for the process group that owns the
// foreground. On the PTY master this is the shell's job while it runs a
// command, and the shell itself while it waits at the prompt.
func foregroundPgrp(f *os.File) (int, error) {
	var pgrp int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&pgrp)))
	if errno != 0 {
		return 0, errno
	}
	return int(pgrp), nil
}

// processName resolves a pid to a short executable name.
func processName(pid int) string {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if arg := strings.SplitN(string(b), "\x00", 2)[0]; arg != "" {
			return filepath.Base(arg)
		}
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	// macOS and other systems without /proc.
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return ""
	}
	return filepath.Base(strings.TrimPrefix(line, "-"))
}

// cwdOf resolves a process's working directory. Linux and Termux expose it in
// /proc; macOS falls back to lsof.
func cwdOf(pid int) string {
	if d, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
		return d
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n")
		}
	}
	return ""
}
