package vault

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tst/internal/session"
)

// instancePath is the runtime socket that makes the vault a singleton.
func instancePath() string {
	// Keep this out of the top-level runtime directory: session.List treats
	// every *.sock file there as a session.
	dir := filepath.Join(session.Dir(), "vault")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "ui.sock")
}

// instance owns the vault singleton socket. A second vault connects to it,
// sends "close", and waits until the first one has restored its terminal.
type instance struct {
	path string
	ln   net.Listener

	once   sync.Once
	closed chan struct{}

	mu    sync.Mutex
	owner bool
}

func acquireInstance(path string) (*instance, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", path); err == nil {
			_, _ = c.Write([]byte("close\n"))
			c.Close()
			time.Sleep(40 * time.Millisecond)
			continue
		}
		_ = os.Remove(path) // stale socket
		ln, err := net.Listen("unix", path)
		if err != nil {
			time.Sleep(40 * time.Millisecond)
			continue
		}
		_ = os.Chmod(path, 0o600)
		i := &instance{path: path, ln: ln, closed: make(chan struct{}), owner: true}
		go i.accept()
		return i, nil
	}
	return nil, fmt.Errorf("another vault is open and did not close")
}

func (i *instance) accept() {
	for {
		conn, err := i.ln.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		if line == "close\n" || line == "close" {
			i.shutdown(false) // taken over: leave the path for the new owner
			return
		}
	}
}

func (i *instance) shutdown(release bool) {
	i.mu.Lock()
	owner := i.owner
	i.owner = false
	i.mu.Unlock()
	i.once.Do(func() { close(i.closed) })
	_ = i.ln.Close()
	if release && owner {
		_ = os.Remove(i.path)
	}
}

// Close releases the singleton.
func (i *instance) Close() { i.shutdown(true) }

func (i *instance) done() <-chan struct{} { return i.closed }
