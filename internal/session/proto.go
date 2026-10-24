// Package session implements persistent sessions: a background server owns
// the panes (so they survive the terminal closing, SSH dropping, ...) and a
// thin client attaches to it over a unix socket, relaying keystrokes in and
// drawn frames out. Detach with the "detach" action, re-attach with `ananta`.
package session

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// Frame types. A frame is: 1 byte type, 4 byte big-endian length, payload.
const (
	// client -> server
	MsgHello  = 'H' // JSON Size; first frame of an attaching client
	MsgInput  = 'I' // raw terminal input bytes
	MsgResize = 'R' // JSON Size
	MsgKill   = 'K' // first frame: end the session
	MsgInfo   = 'P' // first frame: ask for session info
	// server -> client
	MsgSetup    = 'S' // terminal setup sequence, NUL, teardown sequence
	MsgOutput   = 'O' // bytes to write to the terminal
	MsgDetached = 'X' // payload: reason; client should exit
	MsgEnded    = 'Q' // session ended (all panes closed / killed)
	MsgInfoResp = 'p' // JSON Info
)

const maxFrame = 16 << 20

type Size struct{ Cols, Rows int }

type Info struct {
	Name     string
	Panes    int
	Attached bool
	PID      int
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("frame too large (%d bytes)", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,48}$`)

// ValidName reports whether name can be used as a session name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Dir is the private directory holding session sockets and logs.
func Dir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, fmt.Sprintf("ananta-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

func SockPath(name string) string { return filepath.Join(Dir(), name+".sock") }
func LogPath(name string) string  { return filepath.Join(Dir(), name+".log") }
