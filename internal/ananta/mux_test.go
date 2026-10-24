package ananta

import (
	"bufio"
	"io"
	"testing"
)

func testMux(t *testing.T) *Mux {
	t.Helper()
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	m := newMux(cfg, bufio.NewWriter(io.Discard))
	m.W, m.H = 100, 30
	return m
}

func TestHelpScrollBounds(t *testing.T) {
	m := testMux(t)
	m.showHelp = true
	m.helpOffset = 0
	m.scrollHelp(-100)
	if m.helpOffset != 0 {
		t.Fatalf("expected lower bound 0, got %d", m.helpOffset)
	}
	m.scrollHelp(10_000)
	if m.helpOffset != m.helpMaxOffset() {
		t.Fatalf("expected upper bound %d, got %d", m.helpMaxOffset(), m.helpOffset)
	}
}

func TestOverviewHitTest(t *testing.T) {
	m := testMux(t)
	ws := m.curWS()
	ws.Cols = []*Column{
		{Panes: []*Pane{{}, {}}, Focus: 1, WFrac: 0.5, CurW: 50},
		{Panes: []*Pane{{}}, Focus: 0, WFrac: 0.5, CurW: 50},
	}
	ws.Focus = 1
	ws.Overview = true
	ws.OverviewCol, ws.OverviewPane = ws.Focus, ws.Cols[ws.Focus].Focus
	rects := m.overviewPaneRects(ws)
	if len(rects) == 0 {
		t.Fatal("expected overview rects")
	}
	r := rects[0]
	hit, ok := m.hitTestOverview(r.x, r.y)
	if !ok {
		t.Fatal("expected overview hit")
	}
	if hit.col != r.col || hit.pane != r.pane {
		t.Fatalf("unexpected hit: %#v != %#v", hit, r)
	}
}
