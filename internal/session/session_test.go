package session

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"

	"ananta/internal/config"
)

// readUntil reads output frames until want appears (or a non-output frame of
// type stopOn arrives).
func readUntil(t *testing.T, conn net.Conn, want string, stopOn byte) (found bool, last byte) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var acc []byte
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		typ, payload, err := readFrame(conn)
		if err != nil {
			continue
		}
		last = typ
		if typ == MsgOutput {
			acc = append(acc, payload...)
			if bytes.Contains(acc, []byte(want)) {
				return true, typ
			}
		}
		if stopOn != 0 && typ == stopOn {
			return false, typ
		}
	}
	return false, last
}

func attach(t *testing.T, name string) net.Conn {
	t.Helper()
	conn, err := dial(name)
	if err != nil {
		t.Fatal(err)
	}
	hello, _ := json.Marshal(Size{Cols: 80, Rows: 24})
	if err := writeFrame(conn, MsgHello, hello); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestDetachKeepsPanesAlive(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(cfg, "t") }()
	for i := 0; i < 100 && !Exists("t"); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !Exists("t") {
		t.Fatal("server did not start")
	}

	c1 := attach(t, "t")
	if typ, _, err := readFrame(c1); err != nil || typ != MsgSetup {
		t.Fatalf("expected setup frame, got %c %v", typ, err)
	}
	_ = writeFrame(c1, MsgInput, []byte("echo hello-$((40+2))\r"))
	if ok, _ := readUntil(t, c1, "hello-42", 0); !ok {
		t.Fatal("never saw command output")
	}
	_ = writeFrame(c1, MsgInput, []byte("\x1bd")) // detach chord (alt+d)
	if ok, last := readUntil(t, c1, "", MsgDetached); ok || last != MsgDetached {
		t.Fatalf("expected detach notice, last frame %c", last)
	}
	c1.Close()

	time.Sleep(200 * time.Millisecond)
	infos, err := List()
	if err != nil || len(infos) != 1 || infos[0].Attached || infos[0].Panes != 1 {
		t.Fatalf("after detach: %+v err=%v", infos, err)
	}

	c2 := attach(t, "t") // the earlier output must be repainted from server state
	if typ, _, _ := readFrame(c2); typ != MsgSetup {
		t.Fatal("no setup on reattach")
	}
	if ok, _ := readUntil(t, c2, "hello-42", 0); !ok {
		t.Fatal("state lost across detach")
	}
	c2.Close()

	if err := Kill("t"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after kill")
	}
}

func TestFrameRoundTripAndNames(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, MsgInput, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	typ, p, err := readFrame(&buf)
	if err != nil || typ != MsgInput || string(p) != "abc" {
		t.Fatalf("round trip: %c %q %v", typ, p, err)
	}
	for name, ok := range map[string]bool{"main": true, "work-1.a": true, "": false, "a/b": false, "../x": false} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) != %v", name, ok)
		}
	}
}
