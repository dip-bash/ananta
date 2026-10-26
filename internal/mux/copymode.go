package mux

import (
	"unicode"

	"ananta/internal/input"
	"ananta/internal/pane"
)

// CopyMode is a vim-style cursor + visual selection over a pane's history
// and screen (logical rows, see scrollback.go). Keys are consumed here
// instead of being sent to the app.
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
	v := viewLocked(p)
	p.Term.Unlock()
	cm := &CopyMode{P: p, X: cur.X, Y: v.hist + cur.Y}
	if p.Anchored { // already scrolled back: start at the top of what you see
		cm.X, cm.Y = 0, viewTop(p, v)
	}
	m.cm = cm
}

func isBlankRow(row []rune) bool {
	for _, r := range row {
		if r != ' ' && r != 0 {
			return false
		}
	}
	return true
}

func firstNonBlank(row []rune) int {
	for i, r := range row {
		if r != ' ' && r != 0 {
			return i
		}
	}
	return 0
}

func lastNonBlank(row []rune) int {
	for i := len(row) - 1; i >= 0; i-- {
		if row[i] != ' ' && row[i] != 0 {
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

// wordMove implements w/W/b/B/e/E. The buffer is treated as text with a
// newline after every row.
func wordMove(g *grid, x, y *int, kind byte) {
	w := g.cols + 1
	limit := g.total * w
	big := kind == 'W' || kind == 'B' || kind == 'E'
	var cl func(i int) int
	cl = func(i int) int {
		if i%w == g.cols {
			return 0
		}
		r := g.row(i / w)[i%w]
		if r == 0 && i%w > 0 { // second half of a wide character: same class as its first half
			return cl(i - 1)
		}
		return charClass(r, big)
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
	*x = min(g.cols-1, idx%w)
}

// findChar implements f/F/t/T (reverse flips the direction, for , ).
func (cm *CopyMode) findChar(g *grid, op byte, ch rune, reverse bool) {
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
	row := g.row(cm.Y)
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
	g := newGrid(cm.P)
	sel := cm.selection()
	switch {
	case sel != nil && line:
		sel.Kind = 'l'
	case sel == nil:
		sel = &Selection{P: cm.P, AX: 0, AY: cm.Y, CX: g.cols - 1, CY: cm.Y, Kind: 'l'}
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

// cmEnsureVisible scrolls the pane so the copy cursor stays on screen.
func (m *Mux) cmEnsureVisible() {
	cm := m.cm
	p := cm.P
	v := viewOf(p)
	top := viewTop(p, v)
	vis := max(1, v.rows)
	switch {
	case cm.Y < top:
		top = cm.Y
	case cm.Y >= top+vis:
		top = cm.Y - vis + 1
	}
	setTop(p, top, v)
}

func (m *Mux) copyKey(k input.Key) {
	cm := m.cm
	if cm.P.Dead {
		m.cm = nil
		return
	}
	g := newGrid(cm.P)
	rows, cols := g.total, g.cols
	vis := max(1, g.v.rows)
	top := viewTop(cm.P, g.v)
	defer func() {
		if m.cm != nil {
			m.cm.Y = max(0, min(rows-1, m.cm.Y))
			m.cm.X = max(0, min(cols-1, m.cm.X))
			m.cmEnsureVisible()
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
				cm.X = firstNonBlank(g.row(0))
			}
			return
		}
		cm.lastFind, cm.lastCh = op, r
		for i := 0; i < n; i++ {
			cm.findChar(g, op, r, false)
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
	half, page := max(1, vis/2), max(1, vis-1)

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
		cm.X = lastNonBlank(g.row(cm.Y))
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
		cm.X = firstNonBlank(g.row(cm.Y))
	case '$':
		cm.X = lastNonBlank(g.row(cm.Y))
	case 'w', 'W', 'b', 'B', 'e', 'E':
		for i := 0; i < n; i++ {
			wordMove(g, &cm.X, &cm.Y, byte(r))
		}
	case 'g', 'f', 'F', 't', 'T':
		cm.pend = byte(r)
		cm.count = n // keep the count for the second key
		if n == 1 {
			cm.count = 0
		}
	case 'G':
		last := rows - 1
		for last > 0 && isBlankRow(g.row(last)) {
			last--
		}
		cm.Y = last
		cm.X = firstNonBlank(g.row(last))
	case 'H':
		cm.Y = top
	case 'M':
		cm.Y = top + vis/2
	case 'L':
		cm.Y = top + vis - 1
	case '{':
		for i := 0; i < n; i++ {
			y := cm.Y - 1
			for y > 0 && isBlankRow(g.row(y)) {
				y--
			}
			for y > 0 && !isBlankRow(g.row(y)) {
				y--
			}
			cm.Y = max(0, y)
		}
	case '}':
		for i := 0; i < n; i++ {
			y := cm.Y + 1
			for y < rows-1 && isBlankRow(g.row(y)) {
				y++
			}
			for y < rows-1 && !isBlankRow(g.row(y)) {
				y++
			}
			cm.Y = min(rows-1, y)
		}
	case ';', ',':
		if cm.lastFind != 0 {
			for i := 0; i < n; i++ {
				cm.findChar(g, cm.lastFind, cm.lastCh, r == ',')
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
