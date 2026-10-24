package screen

import (
	"bufio"
	"strconv"
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
)

type Cell struct {
	R      rune
	FG, BG Col
	A      uint8
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

func (s *Screen) Text(x, y int, str string, fg, bg Col, a uint8) int {
	for _, r := range str {
		s.Set(x, y, Cell{R: r, FG: fg, BG: bg, A: a})
		x++
	}
	return x
}

// Renderer diffs the new frame against the previous one and emits only the
// changed cells, wrapped in synchronized-update markers so frames never tear.
type Renderer struct {
	out       *bufio.Writer
	prev, cur *Screen
}

func NewRenderer(out *bufio.Writer) *Renderer { return &Renderer{out: out} }

func (r *Renderer) Resize(w, h int) {
	r.cur = NewScreen(w, h)
	r.prev = NewScreen(w, h)
	for i := range r.prev.C {
		r.prev.C[i].R = -1 // force full repaint
	}
	r.out.WriteString("\x1b[2J")
}

func (r *Renderer) color(base int, c Col) {
	if c < 0 {
		return
	}
	w := r.out
	w.WriteByte(';')
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
	r.color(38, c.FG)
	r.color(48, c.BG)
	w.WriteByte('m')
}

func (r *Renderer) Flush(cx, cy int, showCursor bool) {
	w := r.out
	w.WriteString("\x1b[?2026h\x1b[?25l")
	var lastFG, lastBG Col
	var lastA uint8
	sgrOK := false
	lx, ly := -1, -1
	for y := 0; y < r.cur.H; y++ {
		for x := 0; x < r.cur.W; x++ {
			i := y*r.cur.W + x
			c := r.cur.C[i]
			if c == r.prev.C[i] {
				continue
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
		}
	}
	copy(r.prev.C, r.cur.C)
	w.WriteString("\x1b[0m")
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
