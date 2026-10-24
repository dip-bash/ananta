// Package clipboard copies text to the system clipboard. It uses the OSC 52
// terminal escape (works over SSH in most modern terminals) and, in parallel,
// whichever native clipboard tool is installed.
package clipboard

import (
	"encoding/base64"
	"os/exec"
	"strings"
)

// OSC52 returns the escape sequence that asks the terminal to set the
// clipboard. Write it to the terminal.
func OSC52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

var copyTools = [][]string{
	{"wl-copy"},
	{"xclip", "-selection", "clipboard", "-in"},
	{"xsel", "--clipboard", "--input"},
	{"termux-clipboard-set"},
	{"pbcopy"},
}

var pasteTools = [][]string{
	{"wl-paste", "--no-newline"},
	{"xclip", "-selection", "clipboard", "-out"},
	{"xsel", "--clipboard", "--output"},
	{"termux-clipboard-get"},
	{"pbpaste"},
}

// CopyNative pipes text into the first available clipboard tool and reports
// whether one was found.
func CopyNative(text string) bool {
	for _, t := range copyTools {
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

// Paste reads the system clipboard ("" if no tool is available).
func Paste() string {
	for _, t := range pasteTools {
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		if out, err := exec.Command(t[0], t[1:]...).Output(); err == nil {
			return string(out)
		}
	}
	return ""
}
