package pane

import (
	"strings"
	"testing"
	"time"
)

func TestWriteToStuckChildDoesNotBlock(t *testing.T) {
	dirty := make(chan struct{}, 1)
	exits := make(chan *Pane, 4)
	p, err := New(Options{ID: 1, Cmdline: "sleep 30", Cols: 80, Rows: 24, Scrollback: 100}, dirty, exits, nil)
	if err != nil {
		t.Skip("no PTY available here: ", err)
	}
	defer p.Close()
	if err := p.PTY.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Logf("PTY is NOT pollable: %v", err)
	} else {
		t.Log("PTY supports write deadlines")
	}
	start := time.Now()
	big := []byte(strings.Repeat("x", 512*1024)) // `sleep` never reads its tty
	p.Write(big)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("write to a stuck child blocked for %v", d)
	} else {
		t.Logf("returned after %v", d)
	}
}

// Terminal queries (e.g. cursor position, asked for by shells and editors) are answered
// by the emulator writing to the PTY from the reader goroutine. That must keep
// working long after the last keystroke.
func TestEmulatorReplyWorksAfterIdle(t *testing.T) {
	dirty := make(chan struct{}, 8)
	exits := make(chan *Pane, 4)
	p, err := New(Options{ID: 1, Cmdline: "cat", Cols: 80, Rows: 24, Scrollback: 100}, dirty, exits, nil)
	if err != nil {
		t.Skip("no PTY available here: ", err)
	}
	defer p.Close()
	p.Write([]byte("hi\r"))
	time.Sleep(600 * time.Millisecond) // well past any write deadline set by Write
	// pretend the child sent "ESC [ 6 n": the emulator must answer on the PTY,
	// and `cat` echoes whatever arrives back to us
	p.Term.Write([]byte("\x1b[6n"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.Term.Lock()
		got := false
		for y := 0; y < 24 && !got; y++ {
			var sb strings.Builder
			for _, g := range p.Term.Row(y) {
				sb.WriteRune(g.Char)
			}
			got = strings.Contains(sb.String(), "R")
		}
		p.Term.Unlock()
		if got {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("emulator's reply to a terminal query never reached the child (write deadline left expired?)")
}
