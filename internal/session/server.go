package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/mux"
)

type client struct {
	conn net.Conn
	wmu  sync.Mutex
}

func (c *client) send(typ byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return writeFrame(c.conn, typ, payload)
}

// Server owns one mux and serves at most one attached client at a time.
type Server struct {
	name            string
	m               *mux.Mux
	ln              net.Listener
	keys            chan []input.Key
	post            chan func()
	setup, teardown string

	mu        sync.Mutex
	cur       *client
	first     chan Size
	firstOnce sync.Once
	stop      chan struct{}
	stopOnce  sync.Once
}

// clientWriter is where the mux draws: frames go to the attached client and
// are discarded while detached.
type clientWriter struct{ s *Server }

func (w clientWriter) Write(p []byte) (int, error) {
	c := w.s.current()
	if c == nil {
		return len(p), nil
	}
	if err := c.send(MsgOutput, p); err != nil {
		go w.s.drop(c)
	}
	return len(p), nil
}

func (s *Server) current() *client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// drop disconnects c (if it is still the attached client) and stops drawing.
func (s *Server) drop(c *client) {
	s.mu.Lock()
	was := s.cur == c
	if was {
		s.cur = nil
	}
	s.mu.Unlock()
	_ = c.conn.Close()
	if was {
		s.post <- func() {
			if s.current() == nil {
				s.m.SetAttached(false)
			}
		}
	}
}

// detachCurrent runs on the mux goroutine (the "detach" action).
func (s *Server) detachCurrent() {
	s.mu.Lock()
	c := s.cur
	s.cur = nil
	s.mu.Unlock()
	s.m.SetAttached(false)
	if c != nil {
		go func() {
			_ = c.send(MsgDetached, []byte("detached"))
			_ = c.conn.Close()
		}()
	}
}

func (s *Server) requestStop() {
	s.stopOnce.Do(func() { close(s.stop) })
	select {
	case s.post <- s.m.Quit:
	default:
	}
}

func (s *Server) handle(conn net.Conn) {
	typ, payload, err := readFrame(conn)
	if err != nil {
		conn.Close()
		return
	}
	switch typ {
	case MsgInfo:
		s.replyInfo(conn)
	case MsgKill:
		s.requestStop()
		conn.Close()
	case MsgHello:
		var sz Size
		if json.Unmarshal(payload, &sz) != nil || sz.Cols < 1 || sz.Rows < 2 {
			conn.Close()
			return
		}
		s.attach(conn, sz)
	default:
		conn.Close()
	}
}

func (s *Server) replyInfo(conn net.Conn) {
	defer conn.Close()
	ch := make(chan int, 1)
	select {
	case s.post <- func() { ch <- s.m.PaneCount() }:
	default:
	}
	panes := -1
	select {
	case panes = <-ch:
	case <-time.After(500 * time.Millisecond):
	}
	b, _ := json.Marshal(Info{Name: s.name, Panes: panes, Attached: s.current() != nil, PID: os.Getpid()})
	_ = writeFrame(conn, MsgInfoResp, b)
}

func (s *Server) attach(conn net.Conn, sz Size) {
	c := &client{conn: conn}
	s.mu.Lock()
	old := s.cur
	s.cur = c
	s.mu.Unlock()
	if old != nil {
		_ = old.send(MsgDetached, []byte("another client attached"))
		_ = old.conn.Close()
	}
	_ = c.send(MsgSetup, []byte(s.setup+"\x00"+s.teardown))
	s.firstOnce.Do(func() { s.first <- sz })
	s.post <- func() {
		s.m.SetAttached(true)
		s.m.SetSize(sz.Cols, sz.Rows)
	}

	for {
		typ, payload, err := readFrame(conn)
		if err != nil {
			break
		}
		switch typ {
		case MsgInput:
			s.keys <- input.ParseKeys(payload)
		case MsgResize:
			var sz Size
			if json.Unmarshal(payload, &sz) == nil && sz.Cols > 0 && sz.Rows > 1 {
				s.post <- func() { s.m.SetSize(sz.Cols, sz.Rows) }
			}
		}
	}
	s.drop(c)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

// Serve runs a session server in the foreground until the session ends.
// The first pane is created when the first client attaches (its terminal
// size is needed to lay panes out).
func Serve(cfg *config.Config, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid session name %q", name)
	}
	path := SockPath(name)
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return fmt.Errorf("session %q is already running", name)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	defer os.Remove(path)
	defer ln.Close()

	s := &Server{
		name:  name,
		ln:    ln,
		keys:  make(chan []input.Key, 64),
		post:  make(chan func(), 64),
		first: make(chan Size, 1),
		stop:  make(chan struct{}),
	}
	s.setup, s.teardown = cfg.TermModes()
	s.m = mux.New(cfg, bufio.NewWriterSize(clientWriter{s}, 1<<16))
	s.m.OnDetach = s.detachCurrent

	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-term; s.requestStop() }()
	go s.acceptLoop()

	select {
	case sz := <-s.first:
		s.m.SetSize(sz.Cols, sz.Rows)
	case <-s.stop:
		return nil
	case <-time.After(60 * time.Second):
		return nil // nobody ever attached
	}
	s.m.Start()
	s.m.Loop(s.keys, s.post)
	s.m.Shutdown()
	if c := s.current(); c != nil {
		_ = c.send(MsgEnded, nil)
		_ = c.conn.Close()
	}
	return nil
}

// splitSetup splits a MsgSetup payload.
func splitSetup(p []byte) (setup, teardown string) {
	a, b, _ := strings.Cut(string(p), "\x00")
	return a, b
}
