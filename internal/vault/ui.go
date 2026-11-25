package vault

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"tst/internal/config"
	"tst/internal/input"
	"tst/internal/mux"
	"tst/internal/screen"
	"tst/internal/session"
	"tst/internal/width"
)

// Options are the non-config knobs the caller knows about.
type Options struct {
	BootErr    string // failed to start the background session: show it, don't hide it
	ConfigPath string // passed to EnsureServer when creating a session
}

type uiMode int

const (
	modeNormal uiMode = iota
	modeSearch
	modeRename
	modeCreate
)

type rowKind int

const (
	rowSession rowKind = iota
	rowPane
	rowCreate
)

type row struct {
	kind    rowKind
	session string
	pane    Pane
	name    string
	count   int
}

func (r row) key() string {
	switch r.kind {
	case rowSession:
		return "s\x00" + r.session
	case rowPane:
		return "p\x00" + r.session + "\x00" + strconv.Itoa(r.pane.ID)
	}
	return "c"
}

func (r row) height() int {
	if r.kind == rowPane {
		return 2
	}
	return 1
}

type vtheme struct {
	border, focus, marked                    screen.Col
	title, focusTitle                        screen.Col
	statusBG, statusFG, accent, selBG, selFG screen.Col
	warn                                     screen.Col
}

type loadResult struct {
	sessions []Session
	err      error
}

type target struct {
	session string
	pane    int
}

// UI is the vault terminal screen.
type UI struct {
	opt Options
	th  vtheme

	inside string
	local  bool

	rend      *screen.Renderer
	fd        int
	W, H      int
	bootErr   string
	sessions  []Session
	rows      []row
	sel       int
	top       int
	filter    string
	mode      uiMode
	input     textInput
	rename    Pane
	pending   byte
	status    string
	statusAt  time.Time
	loading   bool
	loadingAt time.Time
	loadCh    chan loadResult
	quit      bool
	target    *target
}

func newUI(cfg *config.Config, opt Options) *UI {
	u := &UI{
		opt:     opt,
		loadCh:  make(chan loadResult, 1),
		inside:  os.Getenv("TST_SESSION"),
		local:   os.Getenv("TST") == "1" && os.Getenv("TST_SESSION") == "",
		bootErr: opt.BootErr,
		th: vtheme{
			border: config.ParseColor(cfg.Theme.Border), focus: config.ParseColor(cfg.Theme.FocusBorder),
			marked: config.ParseColor(cfg.Theme.MarkedBorder),
			title:  config.ParseColor(cfg.Theme.Title), focusTitle: config.ParseColor(cfg.Theme.FocusTitle),
			statusBG: config.ParseColor(cfg.Theme.StatusBG), statusFG: config.ParseColor(cfg.Theme.StatusFG),
			accent: config.ParseColor(cfg.Theme.Accent),
			selBG:  config.ParseColor(cfg.Theme.SelectionBG), selFG: config.ParseColor(cfg.Theme.SelectionFG),
			warn: screen.RGB(0xbf, 0x61, 0x6a),
		},
	}
	if u.th.marked == screen.ColDefault {
		u.th.marked = u.th.accent
	}
	return u
}

// Run opens the vault in the current terminal. Outside tstrip it attaches to
// the session the user chooses; inside a pane it asks the parent client to
// switch.
func Run(cfg *config.Config, opt Options) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("tst vault must run in a terminal: %w", err)
	}
	defer tty.Close()

	inst, err := acquireInstance(instancePath())
	if err != nil {
		return err
	}
	defer inst.Close()

	fd := int(tty.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	w, h := 80, 24
	if tw, th, err := term.GetSize(fd); err == nil && tw > 0 && th > 1 {
		w, h = tw, th
	}

	const (
		setup    = "\x1b[?1049h\x1b[?25l"
		teardown = "\x1b[0m\x1b[0 q\x1b[?25h\x1b[?1049l"
	)
	out := bufio.NewWriterSize(tty, 1<<16)
	out.WriteString(setup)
	out.Flush()

	u := newUI(cfg, opt)
	u.fd = fd
	u.W, u.H = w, h
	u.rend = screen.NewRenderer(out)
	u.rend.Resize(w, h)

	stop := make(chan struct{})
	keys := make(chan []input.Key, 256)
	go readKeys(tty, keys, stop)

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	quitSig := make(chan os.Signal, 1)
	signal.Notify(quitSig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	u.render()
	target := u.loop(keys, inst.done(), winch, quitSig)

	signal.Stop(winch)
	signal.Stop(quitSig)
	close(stop)
	out.WriteString(teardown)
	out.Flush()
	_ = term.Restore(fd, old)
	inst.Close()

	if target != nil {
		// The vault read the terminal from its own goroutine; replace the
		// process so no leftover reader eats the session's keystrokes. exec
		// also closes the tty, so there is nothing left to wait for.
		return session.AttachExec(target.session, target.pane, opt.ConfigPath)
	}
	_ = tty.Close()
	return nil
}

func readKeys(f *os.File, out chan<- []input.Key, stop <-chan struct{}) {
	asm := input.NewAssembler(func(ks []input.Key) {
		select {
		case out <- ks:
		case <-stop:
		}
	})
	defer asm.Close()
	buf := make([]byte, 8192)
	deadlineOK := true
	for {
		if deadlineOK {
			if err := f.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
				deadlineOK = false
			}
		}
		n, err := f.Read(buf)
		if n > 0 {
			asm.Feed(buf[:n])
		}
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			return
		}
		select {
		case <-stop:
			return
		default:
		}
	}
}

func (u *UI) loop(keys <-chan []input.Key, closed <-chan struct{}, winch, quitSig <-chan os.Signal) *target {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	u.startLoad()
	for !u.quit {
		select {
		case ks := <-keys:
			for _, k := range ks {
				u.handleKey(k)
			}
			u.render()
		case res := <-u.loadCh:
			u.loading = false
			if res.err != nil {
				u.setStatus(res.err.Error())
			} else {
				u.apply(res.sessions)
			}
			u.render()
		case <-tick.C:
			if u.status != "" && time.Now().After(u.statusAt) {
				u.status = ""
			}
			if u.mode == modeNormal {
				u.startLoad()
			}
			u.render()
		case <-winch:
			if w, h, err := term.GetSize(u.fd); err == nil && w > 0 && h > 1 {
				u.resize(w, h)
			}
			u.render()
		case <-quitSig:
			u.quit = true
		case <-closed:
			u.quit = true
		}
	}
	return u.target
}

func (u *UI) resize(w, h int) {
	u.W, u.H = w, h
	u.rend.Resize(w, h)
}

func (u *UI) startLoad() {
	if u.loading {
		return
	}
	u.loading, u.loadingAt = true, time.Now()
	go func() {
		s, err := Load()
		u.loadCh <- loadResult{sessions: s, err: err}
	}()
}

func (u *UI) apply(sessions []Session) {
	key := u.selectedKey()
	u.sessions = sessions
	u.buildRows()
	if key != "" {
		for i, r := range u.rows {
			if r.key() == key {
				u.sel = i
				return
			}
		}
	}
	u.clampSel()
}

func (u *UI) buildRows() {
	filter := strings.ToLower(strings.TrimSpace(u.filter))
	u.rows = u.rows[:0]
	for _, s := range sessionsByOrder(u.sessions) {
		sMatch := filter == "" || strings.Contains(strings.ToLower(s.Name), filter)
		var panes []Pane
		for _, p := range s.Panes {
			if filter == "" || sMatch ||
				strings.Contains(strings.ToLower(p.Display()), filter) ||
				strings.Contains(strings.ToLower(p.Name), filter) ||
				strings.Contains(strings.ToLower(shortCwd(p.Cwd)), filter) {
				panes = append(panes, p)
			}
		}
		if filter != "" && !sMatch && len(panes) == 0 {
			continue
		}
		u.rows = append(u.rows, row{kind: rowSession, session: s.Name, name: s.Name, count: len(s.Panes)})
		for _, p := range panes {
			u.rows = append(u.rows, row{kind: rowPane, session: s.Name, pane: p, name: p.Display()})
		}
	}
	if filter == "" || strings.Contains("create new session", filter) || strings.Contains("new", filter) {
		u.rows = append(u.rows, row{kind: rowCreate, name: "Create new session"})
	}
}

func sessionsByOrder(s []Session) []Session { return s } // Load already ordered

func (u *UI) selectedKey() string {
	if u.sel < 0 || u.sel >= len(u.rows) {
		return ""
	}
	return u.rows[u.sel].key()
}

func (u *UI) clampSel() {
	if len(u.rows) == 0 {
		u.sel = -1
		return
	}
	if u.sel < 0 {
		u.sel = 0
	}
	if u.sel >= len(u.rows) {
		u.sel = len(u.rows) - 1
	}
}

func (u *UI) current() (row, bool) {
	if u.sel < 0 || u.sel >= len(u.rows) {
		return row{}, false
	}
	return u.rows[u.sel], true
}

func (u *UI) move(d int) {
	if len(u.rows) == 0 {
		return
	}
	u.sel = max(0, min(len(u.rows)-1, u.sel+d))
}

func (u *UI) setStatus(s string) {
	u.status, u.statusAt = s, time.Now().Add(4*time.Second)
}

func (u *UI) listHeight() int { return max(1, u.H-2) }

func (u *UI) activate() {
	r, ok := u.current()
	if !ok {
		return
	}
	switch r.kind {
	case rowSession:
		u.jump(r.session, 0)
	case rowPane:
		u.jump(r.session, r.pane.ID)
	case rowCreate:
		u.mode = modeCreate
		u.input.set("")
		u.setStatus("")
	}
}

func (u *UI) jump(name string, pane int) {
	if name == "" {
		return
	}
	if !session.Exists(name) {
		u.setStatus("session " + name + " is no longer running")
		u.startLoad()
		return
	}
	if u.inside == "" {
		if u.local {
			u.setStatus("cannot switch sessions from tst local mode")
			return
		}
		u.target = &target{session: name, pane: pane}
		u.quit = true
		return
	}
	_, err := session.Control(u.inside, mux.Request{
		Op: "vault_jump", TargetSession: name, TargetPane: pane,
	})
	if err != nil {
		u.setStatus(err.Error())
		u.startLoad()
		return
	}
	u.quit = true
}

func (u *UI) removeSelected() {
	r, ok := u.current()
	if !ok || r.kind != rowPane {
		u.setStatus("select a marked pane first")
		return
	}
	if _, err := session.Control(r.session, mux.Request{Op: "vault_remove", Pane: r.pane.ID}); err != nil {
		u.setStatus(err.Error())
		return
	}
	u.setStatus("removed " + r.pane.Display())
	u.startLoad()
}

func (u *UI) deleteSelected() {
	r, ok := u.current()
	if !ok || r.kind != rowPane {
		u.setStatus("select a marked pane first")
		return
	}
	if _, err := session.Control(r.session, mux.Request{Op: "vault_delete", Pane: r.pane.ID}); err != nil {
		u.setStatus(err.Error())
		return
	}
	u.setStatus("closed " + r.pane.Display())
	u.startLoad()
}

func (u *UI) startRename() {
	r, ok := u.current()
	if !ok || r.kind != rowPane {
		u.setStatus("select a marked pane first")
		return
	}
	u.rename = r.pane
	u.mode = modeRename
	u.input.set(r.pane.Display())
	u.setStatus("")
}

func (u *UI) commitRename() {
	text := strings.TrimSpace(u.input.String())
	if text == u.rename.Name {
		text = "" // revert to the automatic name
	}
	if _, err := session.Control(u.rename.Session, mux.Request{
		Op: "vault_rename", Pane: u.rename.ID, Text: text,
	}); err != nil {
		u.setStatus(err.Error())
	} else {
		u.setStatus("renamed")
	}
	u.mode = modeNormal
	u.startLoad()
}

func (u *UI) commitCreate() {
	name := strings.TrimSpace(u.input.String())
	if name == "" {
		name = "main"
	}
	if !session.ValidName(name) {
		u.setStatus("invalid session name (use letters, digits, . _ -)")
		return
	}
	if err := session.EnsureServer(name, u.opt.ConfigPath); err != nil {
		u.setStatus(err.Error())
		return
	}
	u.mode = modeNormal
	if u.inside == "" {
		if u.local {
			u.setStatus("cannot switch sessions from tst local mode")
			return
		}
		u.target = &target{session: name}
		u.quit = true
		return
	}
	if _, err := session.Control(u.inside, mux.Request{Op: "vault_jump", TargetSession: name}); err != nil {
		u.setStatus(err.Error())
		return
	}
	u.quit = true
}

func (u *UI) handleKey(k input.Key) {
	switch u.mode {
	case modeSearch:
		u.handleSearch(k)
		return
	case modeRename:
		u.handleEditMode(k, u.commitRename)
		return
	case modeCreate:
		u.handleEditMode(k, u.commitCreate)
		return
	}

	if u.pending != 0 {
		if (u.pending == 'r' && k.Name == "r") || (u.pending == 'd' && k.Name == "d") {
			pending := u.pending
			u.pending = 0
			if pending == 'r' {
				u.removeSelected()
			} else {
				u.deleteSelected()
			}
			return
		}
		u.pending = 0
		if k.Name == "esc" {
			return // the first Esc only cancels the confirmation
		}
	}

	switch k.Name {
	case "esc", "ctrl+c":
		u.quit = true
	case "up", "k", "ctrl+p":
		u.move(-1)
	case "down", "j", "ctrl+n":
		u.move(1)
	case "pageup", "ctrl+b":
		u.move(-u.listHeight())
	case "pagedown", "ctrl+f":
		u.move(u.listHeight())
	case "home", "g":
		if len(u.rows) > 0 {
			u.sel = 0
		}
	case "end", "shift+g":
		if len(u.rows) > 0 {
			u.sel = len(u.rows) - 1
		}
	case "enter":
		u.activate()
	case "/":
		u.mode = modeSearch
		u.input.set(u.filter)
	case "e":
		u.startRename()
	case "r":
		if r, ok := u.current(); ok && r.kind == rowPane {
			u.pending = 'r'
			u.setStatus("")
		}
	case "d":
		if r, ok := u.current(); ok && r.kind == rowPane {
			u.pending = 'd'
			u.setStatus("")
		}
	}
}

func (u *UI) handleSearch(k input.Key) {
	switch k.Name {
	case "esc":
		u.mode = modeNormal
		u.filter = ""
		u.buildRows()
		u.clampSel()
		return
	case "enter":
		u.mode = modeNormal
		return
	}
	if u.editKey(k) {
		u.filter = u.input.String()
		u.buildRows()
		u.clampSel()
	}
}

func (u *UI) handleEditMode(k input.Key, commit func()) {
	switch k.Name {
	case "esc":
		u.mode = modeNormal
		u.setStatus("")
		return
	case "enter":
		commit()
		return
	}
	u.editKey(k)
}

// editKey applies the shared line-editing keys and returns whether the input
// changed.
func (u *UI) editKey(k input.Key) bool {
	t := &u.input
	switch k.Name {
	case "left":
		t.cur = max(0, t.cur-1)
		return false
	case "right":
		t.cur = min(len(t.r), t.cur+1)
		return false
	case "home", "ctrl+a":
		t.cur = 0
		return false
	case "end", "ctrl+e":
		t.cur = len(t.r)
		return false
	case "backspace":
		if t.cur > 0 {
			t.r = append(t.r[:t.cur-1], t.r[t.cur:]...)
			t.cur--
			return true
		}
		return false
	case "delete", "ctrl+d":
		if t.cur < len(t.r) {
			t.r = append(t.r[:t.cur], t.r[t.cur+1:]...)
			return true
		}
		return false
	case "ctrl+u":
		if t.cur > 0 {
			t.r = append([]rune(nil), t.r[t.cur:]...)
			t.cur = 0
			return true
		}
		return false
	case "ctrl+w":
		if t.cur > 0 {
			i := t.cur
			for i > 0 && t.r[i-1] == ' ' {
				i--
			}
			for i > 0 && t.r[i-1] != ' ' {
				i--
			}
			t.r = append(t.r[:i], t.r[t.cur:]...)
			t.cur = i
			return true
		}
		return false
	}
	if r, ok := k.Rune(); ok && r >= ' ' {
		t.r = append(t.r, 0)
		copy(t.r[t.cur+1:], t.r[t.cur:])
		t.r[t.cur] = r
		t.cur++
		return true
	}
	return false
}

// ---- drawing

func (u *UI) render() {
	if u.W < 1 || u.H < 2 || u.rend == nil {
		return
	}
	scr := u.rend.Cur()
	scr.Clear()

	// title
	scr.Fill(0, 0, u.W, 1, screen.Cell{R: ' ', FG: u.th.statusFG, BG: u.th.statusBG})
	title := " tst vault "
	scr.Text(0, 0, width.Truncate(title, u.W), u.th.accent, u.th.statusBG, screen.AttrBold)
	marks := 0
	for _, s := range u.sessions {
		marks += len(s.Panes)
	}
	right := fmt.Sprintf(" %d sessions · %d marked ", len(u.sessions), marks)
	if width.String(right)+width.String(title) < u.W {
		scr.Text(u.W-width.String(right), 0, right, u.th.statusFG, u.th.statusBG, 0)
	}

	// footer
	scr.Fill(0, u.H-1, u.W, 1, screen.Cell{R: ' ', FG: u.th.statusFG, BG: u.th.statusBG})
	u.drawFooter(scr)

	starts := make([]int, len(u.rows))
	total := 0
	for i, r := range u.rows {
		starts[i] = total
		total += r.height()
	}
	u.keepVisible(starts, total)

	if len(u.rows) == 0 {
		msg := " no matches "
		scr.Text(max(1, (u.W-width.String(msg))/2), 1+u.listHeight()/2, msg, u.th.title, screen.ColDefault, 0)
	}
	for i, r := range u.rows {
		sy := 1 + starts[i] - u.top
		if sy+r.height() <= 1 || sy >= u.H-1 {
			continue
		}
		u.drawRow(scr, i, sy)
	}
	u.rend.Flush(-1, -1, false, 0)
}

func (u *UI) keepVisible(starts []int, total int) {
	lh := u.listHeight()
	if len(u.rows) == 0 {
		u.top = 0
		return
	}
	u.clampSel()
	start, h := starts[u.sel], u.rows[u.sel].height()
	if start < u.top {
		u.top = start
	}
	if start+h > u.top+lh {
		u.top = start + h - lh
	}
	u.top = max(0, min(u.top, max(0, total-lh)))
}

func (u *UI) drawRow(scr *screen.Screen, i, y int) {
	r := u.rows[i]
	selected := i == u.sel
	fg, bg := u.th.focusTitle, screen.ColDefault
	if selected {
		fg, bg = u.th.selFG, u.th.selBG
	}
	switch r.kind {
	case rowSession:
		u.fillRow(scr, y, bg)
		txt := "▌ " + r.name
		if selected {
			scr.Text(0, y, "▸", u.th.accent, bg, screen.AttrBold)
			scr.Text(1, y, width.Truncate(" "+r.name, u.W-2), fg, bg, screen.AttrBold)
		} else {
			scr.Text(0, y, width.Truncate(txt, u.W), u.th.accent, bg, screen.AttrBold)
		}
		count := fmt.Sprintf(" %d ", r.count)
		if width.String(txt)+width.String(count) < u.W-1 {
			scr.Text(u.W-width.String(count)-1, y, count, u.th.title, bg, 0)
		}
	case rowPane:
		u.fillRow(scr, y, bg)
		u.fillRow(scr, y+1, bg)
		name := r.name
		if selected {
			scr.Text(0, y, "▸", u.th.accent, bg, screen.AttrBold)
			scr.Text(3, y, width.Truncate(name, max(0, u.W-4)), fg, bg, screen.AttrBold)
		} else {
			scr.Text(2, y, width.Truncate("  "+name, max(0, u.W-2)), u.th.focusTitle, bg, 0)
		}
		cwd := shortCwd(r.pane.Cwd)
		cf := u.th.title
		if selected {
			cf = u.th.selFG
		}
		if cwd != "" && y+1 < u.H-1 {
			scr.Text(4, y+1, width.Truncate(cwd, max(0, u.W-5)), cf, bg, 0)
		}
	case rowCreate:
		u.fillRow(scr, y, bg)
		text := "+ Create new session"
		x := 2
		if selected {
			scr.Text(0, y, "▸", u.th.accent, bg, screen.AttrBold)
			scr.Text(x, y, width.Truncate(text, max(0, u.W-x-1)), fg, bg, screen.AttrBold)
		} else {
			scr.Text(x, y, width.Truncate(text, max(0, u.W-x-1)), u.th.marked, bg, 0)
		}
	}
}

func (u *UI) fillRow(scr *screen.Screen, y int, bg screen.Col) {
	if y < 1 || y >= u.H-1 {
		return
	}
	scr.Fill(0, y, u.W, 1, screen.Cell{R: ' ', FG: screen.ColDefault, BG: bg})
}

func (u *UI) drawFooter(scr *screen.Screen) {
	const help = " j/k move · enter open · / search · e rename · rr remove · dd delete · esc close "
	left, right := help, ""
	fg, rfg := u.th.statusFG, u.th.statusFG
	switch u.mode {
	case modeSearch:
		left = " /" + u.input.String() + "▏"
		fg = u.th.accent
		right = " enter keep · esc clear "
	case modeRename:
		left = " rename " + u.rename.Display() + ": " + u.input.String() + "▏"
		fg = u.th.accent
		right = " enter save · esc cancel "
	case modeCreate:
		left = " new session: " + u.input.String() + "▏"
		fg = u.th.accent
		right = " enter create · empty = main · esc cancel "
	}
	if u.pending != 0 && u.mode == modeNormal {
		what := "remove"
		if u.pending == 'd' {
			what = "close"
		}
		right = " press " + string(u.pending) + " again to " + what + " · any other key cancels "
		rfg = u.th.marked
	} else if u.status != "" && time.Now().Before(u.statusAt) {
		right = " " + u.status + " "
		rfg = u.th.accent
	} else if u.loading && time.Since(u.loadingAt) > 400*time.Millisecond {
		right = " loading… "
	} else if u.bootErr != "" {
		right = " " + u.bootErr + " "
		rfg = u.th.warn
	}
	y := u.H - 1
	rw := width.String(right)
	// In an input mode the editable text matters most; drop the hint rather
	// than truncating the field.
	if u.mode != modeNormal && width.String(left)+rw > u.W-2 {
		right, rw = "", 0
	}
	// Narrow panes cannot fit the prompt on the right; put it on the left so
	// confirmations and errors are never invisible.
	if rw > 0 && rw > u.W-2 {
		left, right, rw = right, "", 0
	}
	if rw > 0 {
		scr.Text(u.W-rw-1, y, right, rfg, u.th.statusBG, 0)
	}
	lw := u.W - 1
	if rw > 0 {
		lw = u.W - rw - 2
	}
	scr.Text(1, y, width.Truncate(left, max(0, lw)), fg, u.th.statusBG, 0)
}

// textInput is a small line editor for search / rename / new-session prompts.
type textInput struct {
	r   []rune
	cur int
}

func (t *textInput) set(s string)   { t.r, t.cur = []rune(s), len([]rune(s)) }
func (t *textInput) String() string { return string(t.r) }
