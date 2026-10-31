package screen

import (
	"bufio"
	"strconv"

	"ananta/internal/width"
)

// Col is a terminal color: -1 = default, 0..255 = palette, TrueBit|rgb = truecolor.
type Col int32

const (
	ColDefault Col = -1
	TrueBit        = 0x1000000
)

func RGB(r, g, b uint8) Col { return Col(TrueBit | int32(r)<<16 | int32(g)<<8 | int32(b)) }

const (
	AttrBold uint8 = 1 << iota
	AttrItalic
	AttrUnderline
	AttrReverse
	AttrDim
	AttrStrike
	AttrBlink
)

// Cell widths: a double-width character is a WideLead cell followed by a
// WideCont cell.
const (
	WideLead uint8 = 1
	WideCont uint8 = 2
)

type Cell struct {
	R      rune
	FG, BG Col
	A      uint8
	W      uint8 // 0 normal, WideLead, WideCont
}

var Blank = Cell{R: ' ', FG: ColDefault, BG: ColDefault}

type Screen struct {
	W, H int
	C    []Cell
}

func NewScreen(w, h int) *Screen {
	s := &Screen{W: w, H: h, C: make([]Cell, w*h)}
	s.Clear()
	return s
}

func (s *Screen) Clear() {
	for i := range s.C {
		s.C[i] = Blank
	}
}

func (s *Screen) Set(x, y int, c Cell) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	s.C[y*s.W+x] = c
}

func (s *Screen) Fill(x, y, w, h int, c Cell) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			s.Set(xx, yy, c)
		}
	}
}

// Text writes str starting at (x, y) and returns the next free column.
// Double-width characters take two cells; zero-width ones are skipped.
func (s *Screen) Text(x, y int, str string, fg, bg Col, a uint8) int {
	for _, r := range str {
		switch width.Rune(r) {
		case 0:
			continue
		case 2:
			s.Set(x, y, Cell{R: r, FG: fg, BG: bg, A: a, W: WideLead})
			s.Set(x+1, y, Cell{R: ' ', FG: fg, BG: bg, A: a, W: WideCont})
			x += 2
		default:
			s.Set(x, y, Cell{R: r, FG: fg, BG: bg, A: a})
			x++
		}
	}
	return x
}

// normalize repairs double-width characters that got cut (by a border, the
// screen edge, or a neighbouring pane): a lead without its continuation, or a
// continuation without its lead, becomes a blank so the terminal's column
// accounting can't drift.
func (s *Screen) normalize() {
	for y := 0; y < s.H; y++ {
		row := s.C[y*s.W : (y+1)*s.W]
		for x := range row {
			c := &row[x]
			switch c.W {
			case WideLead:
				if x+1 >= s.W || row[x+1].W != WideCont {
					*c = Cell{R: ' ', FG: ColDefault, BG: c.BG}
				}
			case WideCont:
				if x == 0 || row[x-1].W != WideLead {
					*c = Cell{R: ' ', FG: ColDefault, BG: c.BG}
				}
			}
		}
	}
}

// Renderer diffs the new frame against the previous one and emits only the
// changed cells, wrapped in synchronized-update markers so frames never tear.
type Renderer struct {
	out       *bufio.Writer
	prev, cur *Screen
	clear     bool // emit a screen clear inside the next frame
	lastStyle int  // cursor style last sent to the terminal (-1: unknown)
}

func NewRenderer(out *bufio.Writer) *Renderer { return &Renderer{out: out, lastStyle: -1} }

func (r *Renderer) Resize(w, h int) {
	r.cur = NewScreen(w, h)
	r.prev = NewScreen(w, h)
	for i := range r.prev.C {
		r.prev.C[i].R = -1 // force full repaint
	}
	r.clear = true   // cleared inside the next synchronized frame, so there's no flash
	r.lastStyle = -1 // a new client / size: send the cursor style again
}

func (r *Renderer) color(base int, c Col) {
	if c < 0 {
		return
	}
	w := r.out
	w.WriteByte(';')
	if c >= 0 && c < 16 && c&TrueBit == 0 {
		// the 16 basic colours use their classic codes (30-37, 90-97, 40-47,
		// 100-107): every terminal understands them, and they keep behaving
		// exactly as they do outside ananta (theme palette, bold-as-bright)
		code := base - 8 + int(c) // 38 -> 30.., 48 -> 40..
		if c >= 8 {
			code += 60 - 8 // bright: 90.., 100..
		}
		w.WriteString(strconv.Itoa(code))
		return
	}
	w.WriteString(strconv.Itoa(base))
	if c&TrueBit != 0 {
		w.WriteString(";2;")
		w.WriteString(strconv.Itoa(int(c>>16) & 0xff))
		w.WriteByte(';')
		w.WriteString(strconv.Itoa(int(c>>8) & 0xff))
		w.WriteByte(';')
		w.WriteString(strconv.Itoa(int(c) & 0xff))
	} else {
		w.WriteString(";5;")
		w.WriteString(strconv.Itoa(int(c)))
	}
}

func (r *Renderer) sgr(c Cell) {
	w := r.out
	w.WriteString("\x1b[0")
	if c.A&AttrBold != 0 {
		w.WriteString(";1")
	}
	if c.A&AttrItalic != 0 {
		w.WriteString(";3")
	}
	if c.A&AttrUnderline != 0 {
		w.WriteString(";4")
	}
	if c.A&AttrReverse != 0 {
		w.WriteString(";7")
	}
	if c.A&AttrDim != 0 {
		w.WriteString(";2")
	}
	if c.A&AttrStrike != 0 {
		w.WriteString(";9")
	}
	if c.A&AttrBlink != 0 {
		w.WriteString(";5")
	}
	r.color(38, c.FG)
	r.color(48, c.BG)
	w.WriteByte('m')
}

// Flush sends the changes since the last frame. style is the cursor style
// the focused program asked for (0 default, 1-2 block, 3-4 underline,
// 5-6 bar); it is only sent when it changes.
func (r *Renderer) Flush(cx, cy int, showCursor bool, style int) {
	w := r.out
	r.cur.normalize()
	w.WriteString("\x1b[?2026h\x1b[?25l")
	if r.clear {
		w.WriteString("\x1b[0m\x1b[2J")
		r.clear = false
	}
	var lastFG, lastBG Col
	var lastA uint8
	sgrOK := false
	lx, ly := -1, -1
	for y := 0; y < r.cur.H; y++ {
		for x := 0; x < r.cur.W; x++ {
			i := y*r.cur.W + x
			c := r.cur.C[i]
			if c.W == WideCont || c == r.prev.C[i] {
				continue // continuation cells are drawn by their lead
			}
			if y != ly || x != lx {
				w.WriteString("\x1b[")
				w.WriteString(strconv.Itoa(y + 1))
				w.WriteByte(';')
				w.WriteString(strconv.Itoa(x + 1))
				w.WriteByte('H')
			}
			if !sgrOK || c.FG != lastFG || c.BG != lastBG || c.A != lastA {
				r.sgr(c)
				lastFG, lastBG, lastA, sgrOK = c.FG, c.BG, c.A, true
			}
			ch := c.R
			if ch < 0x20 {
				ch = ' '
			}
			w.WriteRune(ch)
			lx, ly = x+1, y
			if c.W == WideLead {
				lx = x + 2
			}
		}
	}
	copy(r.prev.C, r.cur.C)
	w.WriteString("\x1b[0m")
	if showCursor && style != r.lastStyle {
		w.WriteString("\x1b[" + strconv.Itoa(style) + " q")
		r.lastStyle = style
	}
	if showCursor && cx >= 0 && cy >= 0 && cx < r.cur.W && cy < r.cur.H {
		w.WriteString("\x1b[" + strconv.Itoa(cy+1) + ";" + strconv.Itoa(cx+1) + "H\x1b[?25h")
	}
	w.WriteString("\x1b[?2026l")
	w.Flush()
}

// Cur is the frame being composed; draw into it, then call Flush.
func (r *Renderer) Cur() *Screen { return r.cur }

// Raw writes bytes straight to the terminal (e.g. an OSC 52 clipboard
// sequence) and flushes immediately. Only call it from the render goroutine.
func (r *Renderer) Raw(s string) {
	r.out.WriteString(s)
	r.out.Flush()
}
