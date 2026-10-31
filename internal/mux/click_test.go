package mux

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ananta/internal/config"
	"ananta/internal/input"
)

const clickConfig = `
wallpaper = "none"
animate = false
[bar]
left = ["view", "run", "boom", "plain"]
center = []
right = []
[bar.widgets.view]
text = " view "
on_click_action = "center_mode"
on_right_click_action = "overview"
[bar.widgets.run]
text = " run "
on_click = 'echo "$ANANTA_SESSION" > OUT'
[bar.widgets.boom]
text = " boom "
on_click = 'echo it-broke >&2; exit 3'
[bar.widgets.plain]
text = " plain "
`

func clickMux(t *testing.T, out string) *Mux {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	text := strings.ReplaceAll(clickConfig, "OUT", out)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || len(cfg.Warnings) != 0 {
		t.Fatalf("config: %v %v", err, cfg.Warnings)
	}
	m := New(cfg, bufio.NewWriter(io.Discard))
	m.SetSize(100, 24)
	m.SetSessionName("sess")
	m.drawBar(m.rend.Cur()) // lays out the bar and records the clickable regions
	return m
}

// xOf finds a widget's click region.
func xOf(t *testing.T, m *Mux, widget string) int {
	t.Helper()
	for _, h := range m.tabs {
		if h.widget == widget {
			return h.x0
		}
	}
	t.Fatalf("widget %q has no click region (regions: %+v)", widget, m.tabs)
	return 0
}

func click(m *Mux, x, button int) {
	m.handleMouse(&input.Mouse{Btn: button, X: x, Y: m.barY(), Press: true})
}

func TestClickRunsAnActionLeftAndRight(t *testing.T) {
	m := clickMux(t, "out.txt")
	x := xOf(t, m, "view")
	if m.scrollMode != "fit" {
		t.Fatal("precondition")
	}
	click(m, x, 0) // left: center_mode
	if m.scrollMode != "center" {
		t.Errorf("left click should run on_click_action; scroll mode %q", m.scrollMode)
	}
	if m.overview {
		t.Fatal("overview must not be on yet")
	}
	click(m, x+1, 2) // right: overview
	if !m.overview {
		t.Error("right click should run on_right_click_action")
	}
}

func TestClickRunsACommandWithTheSessionInItsEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	m := clickMux(t, out)
	click(m, xOf(t, m, "run"), 0)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && strings.TrimSpace(string(b)) == "sess" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the click command did not run with ANANTA_SESSION set")
}

func TestFailingClickCommandIsReported(t *testing.T) {
	m := clickMux(t, "out.txt")
	click(m, xOf(t, m, "boom"), 0)
	select {
	case msg := <-m.notifyCh:
		if !strings.Contains(msg, "it-broke") {
			t.Errorf("message %q should include the command's error output", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a failed click command should be reported")
	}
}

func TestWidgetWithoutClickActionsIsNotClickable(t *testing.T) {
	m := clickMux(t, "out.txt")
	for _, h := range m.tabs {
		if h.widget == "plain" {
			t.Fatal("a widget with no on_click* settings must not get a click region")
		}
	}
}

func TestWidgetConfigProblemsAreWarned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("[bar.widgets.a]\non_click = \"true\"\n[bar.widgets.b]\ntext = \"b\"\non_click_action = \"nope\"\n"), 0o644)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(cfg.Warnings, ";")
	if !strings.Contains(all, "widget a has neither command nor text") || !strings.Contains(all, "widget b: unknown action nope") {
		t.Errorf("warnings: %v", cfg.Warnings)
	}
}
