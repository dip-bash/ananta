package main

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key is one decoded key press. Name is a canonical chord such as "alt+h",
// "ctrl+alt+shift+left" or "enter"; Raw is exactly what the terminal sent.
type Key struct {
	Name string
	Raw  []byte
}

func canon(ctrl, alt, shift bool, key string) string {
	var sb strings.Builder
	if ctrl {
		sb.WriteString("ctrl+")
	}
	if alt {
		sb.WriteString("alt+")
	}
	if shift {
		sb.WriteString("shift+")
	}
	sb.WriteString(key)
	return sb.String()
}

var keyAliases = map[string]string{
	"return": "enter", "escape": "esc", "pgup": "pageup", "pgdn": "pagedown",
	"pgdown": "pagedown", "del": "delete", "bs": "backspace", "spc": "space",
	"ins": "insert", "arrowleft": "left", "arrowright": "right", "arrowup": "up", "arrowdown": "down",
}

// normalizeChord turns a user-written chord ("Alt+H", "meta+Return") into the
// canonical form produced by the input parser.
func normalizeChord(s string) string {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "+")
	var key string
	if len(parts) >= 2 && parts[len(parts)-1] == "" {
		key = "+"
		parts = parts[:len(parts)-2]
	} else {
		key = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	var ctrl, alt, shift bool
	for _, m := range parts {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case "ctrl", "control":
			ctrl = true
		case "alt", "meta", "opt", "option":
			alt = true
		case "shift":
			shift = true
		}
	}
	if utf8.RuneCountInString(key) == 1 {
		if key[0] >= 'A' && key[0] <= 'Z' {
			shift = true
			key = strings.ToLower(key)
		}
	} else {
		key = strings.ToLower(key)
		if a, ok := keyAliases[key]; ok {
			key = a
		}
	}
	return canon(ctrl, alt, shift, key)
}

func ctrlName(c byte) string {
	switch c {
	case 0x00:
		return "space"
	case 0x1c, 0x1d, 0x1e, 0x1f:
		return string(rune(c + 0x40))
	}
	return string(rune(c + 0x60))
}

// plainKey decodes a single non-escape key at the start of b.
func plainKey(b []byte) (ctrl, shift bool, key string, size int) {
	c := b[0]
	switch {
	case c == 0x7f:
		return false, false, "backspace", 1
	case c == 0x0d:
		return false, false, "enter", 1
	case c == 0x09:
		return false, false, "tab", 1
	case c < 0x20:
		return true, false, ctrlName(c), 1
	}
	r, sz := utf8.DecodeRune(b)
	if r == utf8.RuneError && sz <= 1 {
		return false, false, "", 1
	}
	switch {
	case r == ' ':
		return false, false, "space", sz
	case r >= 'A' && r <= 'Z':
		return false, true, strings.ToLower(string(r)), sz
	}
	return false, false, string(r), sz
}

var tildeKeys = map[int]string{
	1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageup", 6: "pagedown", 7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8", 20: "f9",
	21: "f10", 23: "f11", 24: "f12",
}

func csiName(params []byte, final byte) string {
	ps := strings.Split(string(params), ";")
	mod := 0
	if len(ps) >= 2 {
		if v, err := strconv.Atoi(ps[1]); err == nil && v > 0 {
			mod = v - 1
		}
	}
	shift, alt, ctrl := mod&1 != 0, mod&2 != 0, mod&4 != 0
	key := ""
	switch final {
	case 'A':
		key = "up"
	case 'B':
		key = "down"
	case 'C':
		key = "right"
	case 'D':
		key = "left"
	case 'H':
		key = "home"
	case 'F':
		key = "end"
	case 'P':
		key = "f1"
	case 'Q':
		key = "f2"
	case 'R':
		key = "f3"
	case 'S':
		key = "f4"
	case 'Z':
		key, shift = "tab", true
	case '~':
		n, _ := strconv.Atoi(ps[0])
		key = tildeKeys[n]
	}
	if key == "" {
		return ""
	}
	return canon(ctrl, alt, shift, key)
}

func ss3Name(c byte) string {
	m := map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end",
		'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4"}
	return m[c]
}

// parseKeys splits a chunk of stdin into key presses. It never drops bytes:
// anything it doesn't understand gets an empty Name and is forwarded verbatim.
func parseKeys(buf []byte) []Key {
	var out []Key
	i := 0
	for i < len(buf) {
		if buf[i] != 0x1b {
			ctrl, shift, key, size := plainKey(buf[i:])
			name := ""
			if key != "" {
				name = canon(ctrl, false, shift, key)
			}
			out = append(out, Key{Name: name, Raw: buf[i : i+size]})
			i += size
			continue
		}
		if i+1 >= len(buf) {
			out = append(out, Key{Name: "esc", Raw: buf[i : i+1]})
			i++
			continue
		}
		switch n := buf[i+1]; {
		case n == '[':
			j := i + 2
			for j < len(buf) && !(buf[j] >= 0x40 && buf[j] <= 0x7e) {
				j++
			}
			if j >= len(buf) {
				out = append(out, Key{Raw: buf[i:]})
				i = len(buf)
				continue
			}
			out = append(out, Key{Name: csiName(buf[i+2:j], buf[j]), Raw: buf[i : j+1]})
			i = j + 1
		case n == 'O' && i+2 < len(buf):
			out = append(out, Key{Name: ss3Name(buf[i+2]), Raw: buf[i : i+3]})
			i += 3
		case n == 0x1b:
			out = append(out, Key{Name: "esc", Raw: buf[i : i+1]})
			i++
		default:
			ctrl, shift, key, size := plainKey(buf[i+1:])
			name := ""
			if key != "" {
				name = canon(ctrl, true, shift, key)
			}
			out = append(out, Key{Name: name, Raw: buf[i : i+1+size]})
			i += 1 + size
		}
	}
	return out
}
