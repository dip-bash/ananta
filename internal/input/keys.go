package input

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ModShift = 1 << iota
	ModAlt
	ModCtrl
	ModSuper
)

// Mouse is a decoded SGR (1006) mouse report. X and Y are 0-based screen cells.
type Mouse struct {
	Btn   int // raw button code incl. modifier / motion / wheel bits
	X, Y  int
	Press bool
}

// Key is one decoded input event. Name is a canonical chord such as "alt+h" or
// "ctrl+alt+shift+left"; Raw is the legacy traditional byte sequence that
// should be forwarded to a pane when the key isn't a shortcut.
type Key struct {
	Name  string
	Raw   []byte
	Mouse *Mouse
}

// canon builds the canonical chord name. Modifier order: ctrl, alt, super, shift.
func Canon(mask int, key string) string {
	var sb strings.Builder
	if mask&ModCtrl != 0 {
		sb.WriteString("ctrl+")
	}
	if mask&ModAlt != 0 {
		sb.WriteString("alt+")
	}
	if mask&ModSuper != 0 {
		sb.WriteString("super+")
	}
	if mask&ModShift != 0 {
		sb.WriteString("shift+")
	}
	sb.WriteString(key)
	return sb.String()
}

func MaskName(mask int) string {
	s := Canon(mask, "")
	return strings.TrimSuffix(s, "+")
}

func modBit(tok string) int {
	switch strings.ToLower(strings.TrimSpace(tok)) {
	case "ctrl", "control":
		return ModCtrl
	case "alt", "meta", "opt", "option":
		return ModAlt
	case "shift":
		return ModShift
	case "super", "cmd", "win", "gui", "hyper":
		return ModSuper
	}
	return 0
}

func ParseMods(s string) int {
	m := 0
	for _, t := range strings.Split(s, "+") {
		m |= modBit(t)
	}
	return m
}

var keyAliases = map[string]string{
	"return": "enter", "escape": "esc", "pgup": "pageup", "pgdn": "pagedown",
	"pgdown": "pagedown", "del": "delete", "bs": "backspace", "spc": "space",
	"ins": "insert", "arrowleft": "left", "arrowright": "right", "arrowup": "up", "arrowdown": "down",
}

// normalizeChord turns a user-written chord ("leader+H", "Super+Return") into
// the canonical form produced by the input parser. "leader" expands to the
// configured leader modifier(s).
func NormalizeChord(s string, leader int) string {
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
	mask := 0
	for _, m := range parts {
		if strings.EqualFold(strings.TrimSpace(m), "leader") {
			mask |= leader
		} else {
			mask |= modBit(m)
		}
	}
	if utf8.RuneCountInString(key) == 1 {
		if key[0] >= 'A' && key[0] <= 'Z' {
			mask |= ModShift
			key = strings.ToLower(key)
		}
	} else {
		key = strings.ToLower(key)
		if a, ok := keyAliases[key]; ok {
			key = a
		}
	}
	return Canon(mask, key)
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
func plainKey(b []byte) (mask int, key string, size int) {
	c := b[0]
	switch {
	case c == 0x7f:
		return 0, "backspace", 1
	case c == 0x0d:
		return 0, "enter", 1
	case c == 0x09:
		return 0, "tab", 1
	case c < 0x20:
		return ModCtrl, ctrlName(c), 1
	}
	r, sz := utf8.DecodeRune(b)
	if r == utf8.RuneError && sz <= 1 {
		return 0, "", 1
	}
	switch {
	case r == ' ':
		return 0, "space", sz
	case r >= 'A' && r <= 'Z':
		return ModShift, strings.ToLower(string(r)), sz
	}
	return 0, string(r), sz
}

var tildeKeys = map[int]string{
	1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageup", 6: "pagedown", 7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8", 20: "f9",
	21: "f10", 23: "f11", 24: "f12",
}

func modsFromParam(s string) int {
	// "5" or "5:1" (the extended protocol adds an event type after a colon)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 0
	}
	return (v - 1) & 0xf // shift alt ctrl super; ignore hyper/meta/caps/num
}

func csiName(params []byte, final byte) string {
	ps := strings.Split(string(params), ";")
	mask := 0
	if len(ps) >= 2 {
		mask = modsFromParam(ps[1])
	}
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
		key, mask = "tab", mask|ModShift
	case '~':
		n, _ := strconv.Atoi(ps[0])
		key = tildeKeys[n]
	}
	if key == "" {
		return ""
	}
	return Canon(mask, key)
}

func ss3Name(c byte) string {
	m := map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left", 'H': "home", 'F': "end",
		'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4"}
	return m[c]
}

// csiUKey decodes an extended-keyboard-protocol "CSI code[:shifted] ; mods u" key
// and also builds the legacy bytes a pane would expect for it.
func csiUKey(params []byte) (name string, raw []byte) {
	ps := strings.Split(string(params), ";")
	sub := strings.Split(ps[0], ":")
	code, err := strconv.Atoi(sub[0])
	if err != nil {
		return "", nil
	}
	shifted := 0
	if len(sub) > 1 {
		shifted, _ = strconv.Atoi(sub[1])
	}
	mask := 0
	if len(ps) >= 2 {
		mask = modsFromParam(ps[1])
	}
	var key string
	var r rune
	switch code {
	case 27:
		key = "esc"
	case 13:
		key = "enter"
	case 9:
		key = "tab"
	case 127:
		key = "backspace"
	case 32:
		key = "space"
	default:
		r = rune(code)
		if shifted > 0 && mask&ModShift != 0 && !unicode.IsLetter(r) {
			r = rune(shifted)
			mask &^= ModShift
		}
		if r < 0x20 || (r >= 57344 && r <= 63743) {
			return "", nil
		}
		key = string(unicode.ToLower(r))
	}
	name = Canon(mask, key)

	// legacy bytes
	if mask&ModSuper != 0 {
		return name, nil
	}
	var b []byte
	switch key {
	case "esc":
		b = []byte{0x1b}
	case "enter":
		b = []byte{'\r'}
	case "tab":
		if mask&ModShift != 0 {
			b = []byte("\x1b[Z")
		} else {
			b = []byte{'\t'}
		}
	case "backspace":
		b = []byte{0x7f}
	case "space":
		if mask&ModCtrl != 0 {
			b = []byte{0}
		} else {
			b = []byte{' '}
		}
	default:
		out := r
		if mask&ModShift != 0 && unicode.IsLetter(r) {
			out = unicode.ToUpper(r)
		}
		if mask&ModCtrl != 0 {
			lr := unicode.ToLower(r)
			switch {
			case lr >= 'a' && lr <= 'z':
				b = []byte{byte(lr-'a') + 1}
			case lr == '[':
				b = []byte{0x1b}
			case lr == '\\':
				b = []byte{0x1c}
			case lr == ']':
				b = []byte{0x1d}
			case lr == '^':
				b = []byte{0x1e}
			case lr == '_':
				b = []byte{0x1f}
			case lr == '@':
				b = []byte{0}
			}
		}
		if b == nil {
			b = []byte(string(out))
		}
	}
	if mask&ModAlt != 0 {
		b = append([]byte{0x1b}, b...)
	}
	return name, b
}

func parseMouse(params []byte, final byte) *Mouse {
	f := strings.Split(string(params[1:]), ";")
	if len(f) != 3 {
		return nil
	}
	b, e1 := strconv.Atoi(f[0])
	x, e2 := strconv.Atoi(f[1])
	y, e3 := strconv.Atoi(f[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return nil
	}
	return &Mouse{Btn: b, X: x - 1, Y: y - 1, Press: final == 'M'}
}

// ParseKeys splits a chunk of stdin into events, treating the chunk as
// complete. It never drops bytes: anything it doesn't understand gets an empty
// Name and is forwarded verbatim. For a live stream use an Assembler instead.
func ParseKeys(buf []byte) []Key {
	keys, _ := parseKeys(buf, false)
	return keys
}

// parseKeys decodes buf. With partial set, an incomplete sequence at the very
// end (lone ESC, unfinished CSI, half a UTF-8 character) is returned as rest
// instead of being decoded, so the caller can wait for more bytes.
func parseKeys(buf []byte, partial bool) (out []Key, rest []byte) {
	i := 0
	for i < len(buf) {
		if buf[i] != 0x1b {
			if partial && buf[i] >= 0x80 && !utf8.FullRune(buf[i:]) {
				return out, buf[i:]
			}
			mask, key, size := plainKey(buf[i:])
			name := ""
			if key != "" {
				name = Canon(mask, key)
			}
			out = append(out, Key{Name: name, Raw: buf[i : i+size]})
			i += size
			continue
		}
		if i+1 >= len(buf) {
			if partial {
				return out, buf[i:]
			}
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
				if partial {
					return out, buf[i:]
				}
				out = append(out, Key{Raw: buf[i:]})
				i = len(buf)
				continue
			}
			params, final := buf[i+2:j], buf[j]
			rawSeq := buf[i : j+1]
			switch {
			case len(params) > 0 && params[0] == '<' && (final == 'M' || final == 'm'):
				if mo := parseMouse(params, final); mo != nil {
					out = append(out, Key{Mouse: mo})
				}
			case final == 'u' && (len(params) == 0 || (params[0] >= '0' && params[0] <= '9')):
				name, raw := csiUKey(params)
				out = append(out, Key{Name: name, Raw: raw})
			default:
				out = append(out, Key{Name: csiName(params, final), Raw: rawSeq})
			}
			i = j + 1
		case n == 'O' && i+2 >= len(buf) && partial:
			return out, buf[i:]
		case n == 'O' && i+2 < len(buf):
			out = append(out, Key{Name: ss3Name(buf[i+2]), Raw: buf[i : i+3]})
			i += 3
		case n == 0x1b:
			out = append(out, Key{Name: "esc", Raw: buf[i : i+1]})
			i++
		default:
			if partial && n >= 0x80 && !utf8.FullRune(buf[i+1:]) {
				return out, buf[i:]
			}
			mask, key, size := plainKey(buf[i+1:])
			name := ""
			if key != "" {
				name = Canon(mask|ModAlt, key)
			}
			out = append(out, Key{Name: name, Raw: buf[i : i+1+size]})
			i += 1 + size
		}
	}
	return out, nil
}

// Assembler turns a stream of arbitrarily chunked terminal input into keys.
// A chunk boundary can fall in the middle of an escape sequence (common over
// SSH, and with fast mouse movement); the unfinished tail is held back until
// the rest arrives, or flushed as-is after a short pause (so a lone Escape
// key still works).
type Assembler struct {
	out func([]Key)

	mu     sync.Mutex
	carry  []byte
	gen    int
	timer  *time.Timer
	closed bool
}

// How long an unfinished tail is held back. A lone Escape (the Esc key, or
// the first byte of a sequence cut right after it) must react quickly; an
// unfinished "ESC [ ..." or half a UTF-8 character can safely wait for a slow
// link to deliver the rest.
const (
	loneEscDelay = 40 * time.Millisecond
	partialDelay = 400 * time.Millisecond
)

func NewAssembler(out func([]Key)) *Assembler { return &Assembler{out: out} }

// Feed adds a chunk of input; complete keys are delivered via the callback.
func (a *Assembler) Feed(data []byte) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	buf := append(append([]byte(nil), a.carry...), data...)
	a.gen++
	keys, rest := parseKeys(buf, true)
	if len(rest) > 64 { // not a real sequence; don't hold garbage back
		more, _ := parseKeys(rest, false)
		keys, rest = append(keys, more...), nil
	}
	a.carry = append([]byte(nil), rest...)
	if a.timer != nil {
		a.timer.Stop()
	}
	if len(rest) > 0 {
		gen := a.gen
		delay := partialDelay
		if len(rest) == 1 && rest[0] == 0x1b {
			delay = loneEscDelay
		}
		a.timer = time.AfterFunc(delay, func() { a.flush(gen) })
	}
	a.mu.Unlock()
	if len(keys) > 0 {
		a.out(keys)
	}
}

func (a *Assembler) flush(gen int) {
	a.mu.Lock()
	if a.closed || a.gen != gen || len(a.carry) == 0 {
		a.mu.Unlock()
		return
	}
	keys, _ := parseKeys(a.carry, false)
	a.carry = nil
	a.mu.Unlock()
	if len(keys) > 0 {
		a.out(keys)
	}
}

// Close stops delivering keys.
func (a *Assembler) Close() {
	a.mu.Lock()
	a.closed = true
	if a.timer != nil {
		a.timer.Stop()
	}
	a.mu.Unlock()
}

// Rune returns the printable character this key press produced, if any
// (letters, digits, symbols; not ctrl/alt chords or special keys).
func (k Key) Rune() (rune, bool) {
	if len(k.Raw) == 0 || k.Raw[0] < 0x20 || k.Raw[0] == 0x7f {
		return 0, false
	}
	r, size := utf8.DecodeRune(k.Raw)
	if r == utf8.RuneError || size != len(k.Raw) {
		return 0, false
	}
	return r, true
}
