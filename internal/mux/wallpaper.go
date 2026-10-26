package mux

import (
	"os"
	"time"

	"ananta/internal/pane"
	"ananta/internal/screen"
)

// The wallpaper is one extra pane, running the command from the wallpaper
// file at full screen size. It is not part of any workspace, never receives
// input, and is drawn first so that real panes cover it.

func (m *Mux) startWallpaper() {
	spec := m.cfg.WallpaperSpec
	if spec == nil || spec.Command == "" || m.W < 1 {
		return
	}
	dir := spec.Dir
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	m.nextID++
	p, err := pane.New(pane.Options{
		ID: m.nextID, Cmdline: spec.Command, Dir: dir,
		Cols: m.W, Rows: m.tileH(),
		Env: []string{"ANANTA_WALLPAPER=1"},
	}, m.dirty, m.exits, nil) // no scrollback; it may not touch the clipboard
	if err != nil {
		m.notify("wallpaper failed: " + err.Error())
		return
	}
	m.wall = p
}

// wallpaperExited is called when the wallpaper's command ends. Its last frame
// stays on screen; with restart = true it is started again shortly.
func (m *Mux) wallpaperExited(p *pane.Pane) {
	p.Close()
	if m.cfg.WallpaperSpec != nil && m.cfg.WallpaperSpec.Restart {
		time.AfterFunc(2*time.Second, func() {
			select {
			case m.wallRestart <- struct{}{}:
			default:
			}
		})
	}
}

// drawWallpaper paints the wallpaper into the rectangle (x, y, w, h). The
// rectangle is the full tile area normally, and a shorter row in the overview,
// where the wallpaper is scaled down like everything else.
func (m *Mux) drawWallpaper(scr *screen.Screen, x, y, w, h int) {
	p := m.wall
	if p == nil || w <= 0 || h <= 0 {
		return
	}
	dim := m.cfg.WallpaperSpec != nil && m.cfg.WallpaperSpec.Dim
	p.Term.Lock()
	defer p.Term.Unlock()
	cols, rows := p.Term.Size()
	exact := w == cols && h == rows
	for row := 0; row < h; row++ {
		sy := row
		if !exact {
			sy = row * rows / h
		}
		if sy >= rows {
			continue
		}
		for col := 0; col < w; col++ {
			sx := col
			if !exact {
				sx = col * cols / w
			}
			if sx >= cols {
				continue
			}
			c := glyphCell(p.Term.Cell(sx, sy))
			if !exact {
				thumbFix(&c)
			}
			if dim {
				c.A |= screen.AttrDim
			}
			scr.Set(x+col, y+row, c)
		}
	}
}
