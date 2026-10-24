package mux

import (
	"unicode"

	"ananta/internal/input"
	"ananta/internal/pane"
)

// CopyMode is a vim-style cursor + visual selection over a pane's visible
// screen. Keys are consumed here instead of being sent to the app.
type CopyMode struct {
	P        *pane.Pane
	X, Y     int  // cursor
	Vis      byte // 0 none, 'c' char, 'l' line, 'b' block
	AX, AY   int  // visual anchor
	count    int
	pend     byte // pending first key of g / f / F / t / T
	lastFind byte
	lastCh   rune
}

func (cm *CopyMode) selection() *Selection {
	if cm.Vis == 0 {
		return nil
	}
	return &Selection{P: cm.P, AX: cm.AX, AY: cm.AY, CX: cm.X, CY: cm.Y, Kind: cm.Vis}
}

func (cm *CopyMode) label() string {
	switch cm.Vis {
	case 'c':
		return "VISUAL"
	case 'l':
		return "V-LINE"
	case 'b':
		return "V-BLOCK"
	}
	return "COPY"
}

func (m *Mux) enterCopyMode() {
	p := m.focusedPane()
	if p == nil {
		return
	}
	m.overview, m.showHelp = false, false
	p.Term.Lock()
	cur := p.Term.Cursor()
	p.Term.Unlock()
	m.cm = &CopyMode{P: p, X: cur.X, Y: cur.Y}
}

func isBlankRow(row []rune) bool {
	for _, r := range row {
		if r != ' ' {
			return false
		}
	}
	return true
}

func firstNonBlank(row []rune) int {
	for i, r := range row {
		if r != ' ' {
			return i
		}
	}
	return 0
}

func lastNonBlank(row []rune) int {
	for i := len(row) - 1; i >= 0; i-- {
		if row[i] != ' ' {
			return i
		}
	}
	return 0
}

func charClass(r rune, big bool) int {
	switch {
	case r == ' ' || r == '\n' || r == '\t' || r == 0:
		return 0
	case big, unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
		return 1
	}
	return 2
}

// wordMove implements w/W/b/B/e/E. The grid is treated as text with a
// newline after every row.
func wordMove(grid [][]rune, x, y *int, kind byte) {
	cols, rows := len(grid[0]), len(grid)
	w := cols + 1
	limit := rows * w
	big := kind == 'W' || kind == 'B' || kind == 'E'
	cl := func(i int) int {
		if i%w == cols {
			return 0
		}
		return charClass(grid[i/w][i%w], big)
	}
	idx := *y*w + *x
	switch kind {
	case 'w', 'W':
		if c := cl(idx); c != 0 {
			for idx < limit && cl(idx) == c {
				idx++
			}
		}
		for idx < limit && cl(idx) == 0 {
			idx++
		}
	case 'e', 'E':
		idx++
		for idx < limit && cl(idx) == 0 {
			idx++
		}
		if idx < limit {
			c := cl(idx)
			for idx+1 < limit && cl(idx+1) == c {
				idx++
			}
		}
	case 'b', 'B':
		idx--
		for idx > 0 && cl(idx) == 0 {
			idx--
		}
		if idx > 0 {
			c := cl(idx)
			for idx > 0 && cl(idx-1) == c {
				idx--
			}
		}
	}
	idx = max(0, min(limit-1, idx))
	*y = idx / w
	*x = min(cols-1, idx%w)
}

// findChar implements f/F/t/T (reverse flips the direction, for , ).
func (cm *CopyMode) findChar(grid [][]rune, op byte, ch rune, reverse bool) {
	if reverse {
		switch op {
		case 'f':
			op = 'F'
		case 'F':
			op = 'f'
		case 't':
			op = 'T'
		case 'T':
			op = 't'
		}
	}
	row := grid[cm.Y]
	if op == 'f' || op == 't' {
		for x := cm.X + 1; x < len(row); x++ {
			if row[x] == ch {
				cm.X = x
				if op == 't' {
					cm.X = x - 1
				}
				return
			}
		}
		return
	}
	for x := cm.X - 1; x >= 0; x-- {
		if row[x] == ch {
			cm.X = x
			if op == 'T' {
				cm.X = x + 1
			}
			return
		}
	}
}

func (m *Mux) yank(line bool) {
	cm := m.cm
	grid := snapshot(cm.P)
	cols := 1
	if len(grid) > 0 {
		cols = len(grid[0])
	}
	sel := cm.selection()
	switch {
	case sel != nil && line:
		sel.Kind = 'l'
	case sel == nil:
		sel = &Selection{P: cm.P, AX: 0, AY: cm.Y, CX: cols - 1, CY: cm.Y, Kind: 'l'}
	}
	m.copyText(m.extract(sel))
	m.cm = nil
}

func (m *Mux) toggleVisual(kind byte) {
	cm := m.cm
	if cm.Vis == kind {
		cm.Vis = 0
		return
	}
	if cm.Vis == 0 {
		cm.AX, cm.AY = cm.X, cm.Y
	}
	cm.Vis = kind
}

func (m *Mux) copyKey(k input.Key) {
	cm := m.cm
	if cm.P.Dead {
		m.cm = nil
		return
	}
	grid := snapshot(cm.P)
	rows := len(grid)
	if rows == 0 || len(grid[0]) == 0 {
		m.cm = nil
		return
	}
	cols := len(grid[0])
	defer func() {
		if m.cm != nil {
			m.cm.Y = max(0, min(rows-1, m.cm.Y))
			m.cm.X = max(0, min(cols-1, m.cm.X))
		}
	}()

	if a, ok := m.cfg.Keymap[k.Name]; ok && a.Name == "copy_mode" {
		m.cm = nil // the entry key also leaves the mode
		return
	}
	r, isRune := k.Rune()

	// second key of g / f / F / t / T
	if cm.pend != 0 {
		op := cm.pend
		cm.pend = 0
		n := max(1, cm.count)
		cm.count = 0
		if k.Name == "esc" || !isRune {
			return
		}
		if op == 'g' {
			if r == 'g' {
				cm.Y = 0
				cm.X = firstNonBlank(grid[0])
			}
			return
		}
		cm.lastFind, cm.lastCh = op, r
		for i := 0; i < n; i++ {
			cm.findChar(grid, op, r, false)
		}
		return
	}

	// counts: 3j, 12w ...  (a lone 0 is the "start of line" motion)
	if isRune && ((r >= '1' && r <= '9') || (r == '0' && cm.count > 0)) {
		cm.count = cm.count*10 + int(r-'0')
		return
	}
	n := max(1, cm.count)
	cm.count = 0
	half, page := max(1, rows/2), max(1, rows-1)

	switch k.Name {
	case "esc":
		m.cm = nil
		return
	case "enter":
		m.yank(false)
		return
	case "left":
		cm.X -= n
	case "right":
		cm.X += n
	case "up":
		cm.Y -= n
	case "down":
		cm.Y += n
	case "home":
		cm.X = 0
	case "end":
		cm.X = lastNonBlank(grid[cm.Y])
	case "ctrl+u":
		cm.Y -= half * n
	case "ctrl+d":
		cm.Y += half * n
	case "ctrl+b", "pageup":
		cm.Y -= page * n
	case "ctrl+f", "pagedown":
		cm.Y += page * n
	case "ctrl+v":
		m.toggleVisual('b')
	}
	if !isRune {
		return
	}

	switch r {
	case 'q':
		m.cm = nil
	case 'h':
		cm.X -= n
	case 'l', ' ':
		cm.X += n
	case 'k':
		cm.Y -= n
	case 'j':
		cm.Y += n
	case '0':
		cm.X = 0
	case '^':
		cm.X = firstNonBlank(grid[cm.Y])
	case '$':
		cm.X = lastNonBlank(grid[cm.Y])
	case 'w', 'W', 'b', 'B', 'e', 'E':
		for i := 0; i < n; i++ {
			wordMove(grid, &cm.X, &cm.Y, byte(r))
		}
	case 'g', 'f', 'F', 't', 'T':
		cm.pend = byte(r)
		cm.count = n // keep the count for the second key
		if n == 1 {
			cm.count = 0
		}
	case 'G':
		last := rows - 1
		for last > 0 && isBlankRow(grid[last]) {
			last--
		}
		cm.Y = last
		cm.X = firstNonBlank(grid[last])
	case 'H':
		cm.Y = 0
	case 'M':
		cm.Y = rows / 2
	case 'L':
		cm.Y = rows - 1
	case '{':
		for i := 0; i < n; i++ {
			y := cm.Y - 1
			for y > 0 && isBlankRow(grid[y]) {
				y--
			}
			for y > 0 && !isBlankRow(grid[y]) {
				y--
			}
			cm.Y = max(0, y)
		}
	case '}':
		for i := 0; i < n; i++ {
			y := cm.Y + 1
			for y < rows-1 && isBlankRow(grid[y]) {
				y++
			}
			for y < rows-1 && !isBlankRow(grid[y]) {
				y++
			}
			cm.Y = min(rows-1, y)
		}
	case ';', ',':
		if cm.lastFind != 0 {
			for i := 0; i < n; i++ {
				cm.findChar(grid, cm.lastFind, cm.lastCh, r == ',')
			}
		}
	case 'v':
		m.toggleVisual('c')
	case 'V':
		m.toggleVisual('l')
	case 'o', 'O':
		if cm.Vis != 0 {
			cm.AX, cm.X = cm.X, cm.AX
			cm.AY, cm.Y = cm.Y, cm.AY
		}
	case 'y':
		m.yank(false)
	case 'Y':
		m.yank(true)
	}
}
