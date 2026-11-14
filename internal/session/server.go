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
	"sync/atomic"
	"syscall"
	"time"

	"ananta/internal/config"
	"ananta/internal/input"
	"ananta/internal/mux"
)

// maxBacklog bounds what is queued for a client that has stopped reading (a stalled SSH link, a terminal that stopped
// drawing, a stopped process). Past it the backlog is dropped and the client gets a full repaint once it reads again.
const maxBacklog = 4 << 20

// client is one attached terminal. Frames are queued and written by the client's own goroutine (writeLoop), so a
// client that stops reading never blocks the mux.
type client struct {
	conn net.Conn
	done chan struct{} // closed when writeLoop returns

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []byte // whole frames, ready to write
	stale   bool   // backlog dropped: discard output until the client reads again, then repaint
	closing bool   // write what is queued, then close
	closed  bool
}

func newClient(conn net.Conn) *client {
	c := &client{conn: conn, done: make(chan struct{})}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// send queues a frame. It never blocks.
func (c *client) send(typ byte, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || c.closed || c.stale {
		return
	}

	if len(c.queue)+5+len(payload) > maxBacklog {
		c.queue = c.queue[:0]
		c.stale = true
	} else {
		c.queue = appendFrame(c.queue, typ, payload)
	}
	c.cond.Signal()
}

// finish queues a last frame (detached, session ended) and closes the connection once it is written. A client that
// isn't reading is cut off after 3 seconds.
func (c *client) finish(typ byte, payload []byte) {
	c.mu.Lock()
	if c.closing || c.closed {
		c.mu.Unlock()
		return
	}
	c.queue = appendFrame(c.queue, typ, payload)
	c.closing = true
	c.cond.Signal()
	c.mu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
}

// close drops the connection at once.
func (c *client) close() {
	c.mu.Lock()
	c.closed = true
	c.cond.Signal()
	c.mu.Unlock()

	_ = c.conn.Close()
}

// writeLoop writes queued frames until the client is closed or finished, and returns the write error that ended it,
// if any. resync is called when a client whose backlog was dropped is reading again.
func (c *client) writeLoop(resync func()) error {
	defer close(c.done)
	defer c.conn.Close()

	var buf []byte
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.stale && !c.closing && !c.closed {
			c.cond.Wait()
		}
		if c.closed || (c.closing && len(c.queue) == 0) {
			c.mu.Unlock()
			return nil
		}
		if c.stale && len(c.queue) == 0 {
			c.stale = false
			// CAN aborts any escape sequence the dropped backlog cut in half
			c.queue = appendFrame(c.queue, MsgOutput, []byte("\x18"))
			c.mu.Unlock()
			resync()
			continue
		}
		buf, c.queue = c.queue, buf[:0]
		c.mu.Unlock()

		if _, err := c.conn.Write(buf); err != nil {
			return err
		}
	}
}

// Server owns one mux and serves at most one attached client at a time.
type Server struct {
	name            string
	m               *mux.Mux
	ln              net.Listener
	keys            chan []input.Key
	post            chan func()
	setup, teardown string

	started atomic.Bool // the mux loop is running (a first client has attached)

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
	if c := w.s.current(); c != nil {
		c.send(MsgOutput, p)
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
	c.close()
	if was {
		// from a goroutine: drop can run on the mux goroutine (while drawing),
		// which must never block on its own work queue
		go func() {
			s.post <- func() {
				if s.current() == nil {
					s.m.SetAttached(false)
				}
			}
		}()
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
		c.finish(MsgDetached, []byte("detached"))
	}
}

func (s *Server) requestStop() {
	s.stopOnce.Do(func() { close(s.stop) })
	// a blocking send (from a goroutine): if the queue is momentarily full the
	// quit waits its turn instead of being dropped
	go func() { s.post <- s.m.Quit }()
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
	case MsgCommand:
		s.replyCommand(conn, payload)
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
	c := newClient(conn)
	c.send(MsgSetup, []byte(s.setup+"\x00"+s.teardown)) // queued before the mux can draw to c
	go func() {
		// from a goroutine: the writer must never wait on the mux's work queue
		resync := func() { go func() { s.post <- s.m.Redraw }() }
		if err := c.writeLoop(resync); err != nil {
			s.drop(c)
		}
	}()

	s.mu.Lock()
	old := s.cur
	s.cur = c
	s.mu.Unlock()
	if old != nil {
		old.finish(MsgDetached, []byte("another client attached"))
	}
	s.firstOnce.Do(func() { s.first <- sz })
	s.post <- func() {
		if sz.Cwd != "" {
			s.m.SetStartDir(sz.Cwd)
		}
		s.m.SetAttached(true)
		s.m.SetSize(sz.Cols, sz.Rows)
		s.m.QueryHostColors()
	}
	asm := input.NewAssembler(func(ks []input.Key) { s.keys <- ks })
	defer asm.Close()

	for {
		typ, payload, err := readFrame(conn)
		if err != nil {
			break
		}
		switch typ {
		case MsgInput:
			asm.Feed(payload) // holds back a sequence cut in half by the network
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
		keys:  make(chan []input.Key, 1024),
		post:  make(chan func(), 64),
		first: make(chan Size, 1),
		stop:  make(chan struct{}),
	}
	s.setup, s.teardown = cfg.TermModes()
	s.m = mux.New(cfg, bufio.NewWriterSize(clientWriter{s}, 1<<16))
	s.m.OnDetach = s.detachCurrent
	s.m.SetSessionName(name)

	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-term; s.requestStop() }()
	go s.acceptLoop()

	select {
	case sz := <-s.first:
		s.m.SetStartDir(sz.Cwd)
		s.m.SetSize(sz.Cols, sz.Rows)
	case <-s.stop:
		return nil
	case <-time.After(60 * time.Second):
		return nil // nobody ever attached
	}
	s.m.Start()
	s.started.Store(true)
	s.m.Loop(s.keys, s.post)
	s.m.Shutdown()
	if c := s.current(); c != nil {
		c.finish(MsgEnded, nil)
		<-c.done
	}
	return nil
}

// splitSetup splits a MsgSetup payload.
func splitSetup(p []byte) (setup, teardown string) {
	a, b, _ := strings.Cut(string(p), "\x00")
	return a, b
}

// replyCommand executes a control request on the mux goroutine and answers it.
func (s *Server) replyCommand(conn net.Conn, payload []byte) {
	defer conn.Close()
	var req mux.Request
	resp := Response{}
	switch {
	case json.Unmarshal(payload, &req) != nil:
		resp.Error = "malformed command"
	case !s.started.Load():
		resp.Error = "session is not ready: attach to it once first (run `ananta`)"
	default:
		ch := make(chan Response, 1)
		work := func() {
			out, err := s.m.Control(req)
			r := Response{Output: out}
			if err != nil {
				r.Error = err.Error()
			}
			ch <- r
		}
		select {
		case s.post <- work:
			select {
			case resp = <-ch:
			case <-time.After(5 * time.Second):
				resp.Error = "timed out waiting for the session"
			}
		case <-time.After(2 * time.Second):
			resp.Error = "session is busy"
		}
	}
	b, _ := json.Marshal(resp)
	_ = writeFrame(conn, MsgCommandResp, b)
}
