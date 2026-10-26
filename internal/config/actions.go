package config

import (
	"ananta/internal/width"
	"sort"
	"strconv"
	"strings"
)

// Action is a bound command, e.g. {Name: "goto_workspace", Arg: "2"}.
type Action struct{ Name, Arg string }

// ActionInfo is the single source of truth for action names and the
// descriptions shown in the help screen (in this order).
var ActionInfo = []struct{ Name, Desc string }{
	{"focus_left", "Focus column left"},
	{"focus_right", "Focus column right"},
	{"focus_up", "Focus pane above"},
	{"focus_down", "Focus pane below"},
	{"move_left", "Move column left"},
	{"move_right", "Move column right"},
	{"move_up", "Move pane up"},
	{"move_down", "Move pane down"},
	{"new_pane", "New column"},
	{"new_pane_below", "New pane below"},
	{"close_pane", "Close pane"},
	{"stack_left", "Stack into left column"},
	{"unstack", "Pop out into own column"},
	{"cycle_width", "Cycle column width"},
	{"maximize", "Maximize / restore column"},
	{"grow", "Widen column"},
	{"shrink", "Narrow column"},
	{"center_mode", "Toggle centered scrolling"},
	{"first_column", "First column"},
	{"last_column", "Last column"},
	{"overview", "Toggle overview"},
	{"workspace_next", "Next workspace"},
	{"workspace_prev", "Previous workspace"},
	{"goto_workspace", "Go to workspace"},
	{"move_to_workspace", "Move pane to workspace"},
	{"copy_mode", "Copy mode (select with the keyboard)"},
	{"scroll_up", "Scroll back through history"},
	{"scroll_down", "Scroll forward"},
	{"detach", "Detach (session keeps running)"},
	{"toggle_bar", "Show / hide status bar"},
	{"help", "Toggle this help"},
	{"quit", "Quit"},
}

func ActionIndex(name string) int {
	for i, a := range ActionInfo {
		if a.Name == name {
			return i
		}
	}
	return len(ActionInfo)
}

// KeyFor returns the shortest chord bound to an argument-less action.
func (c *Config) KeyFor(action string) string {
	best := ""
	for k, a := range c.Keymap {
		if a.Name == action && a.Arg == "" && (best == "" || len(k) < len(best) || (len(k) == len(best) && k < best)) {
			best = k
		}
	}
	return best
}

func truncate(s string, n int) string { return width.Truncate(s, n) }

// HelpLines builds the help screen from the live keymap (one line per
// action), so it always reflects the user's config.
func (c *Config) HelpLines() []string {
	type entry struct {
		name, arg string
		keys      []string
	}
	byspec := map[string]*entry{}
	add := func(key string, a Action) {
		spec := a.Name + " " + a.Arg
		e := byspec[spec]
		if e == nil {
			e = &entry{name: a.Name, arg: a.Arg}
			byspec[spec] = e
		}
		e.keys = append(e.keys, key)
	}
	for k, a := range c.Keymap {
		add(k, a)
	}
	for k, a := range c.PrefixMap {
		add(c.PrefixKey+" "+k, a)
	}
	var es []*entry
	for _, e := range byspec {
		sort.Strings(e.keys)
		es = append(es, e)
	}
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if ai, bi := ActionIndex(a.name), ActionIndex(b.name); ai != bi {
			return ai < bi
		}
		an, ae := strconv.Atoi(a.arg)
		bn, be := strconv.Atoi(b.arg)
		if ae == nil && be == nil {
			return an < bn
		}
		return a.arg < b.arg
	})
	kw := 0
	keystr := make([]string, len(es))
	for i, e := range es {
		keystr[i] = strings.Join(e.keys, ", ")
		kw = max(kw, width.String(keystr[i]))
	}
	kw = min(kw, 34)
	var out []string
	for i, e := range es {
		desc := e.name
		if idx := ActionIndex(e.name); idx < len(ActionInfo) {
			desc = ActionInfo[idx].Desc
		}
		if e.arg != "" {
			if e.name == "goto_workspace" || e.name == "move_to_workspace" {
				desc += " " + e.arg
			} else {
				desc += ": " + e.arg
			}
		}
		k := truncate(keystr[i], kw)
		out = append(out, k+strings.Repeat(" ", max(0, kw-width.String(k)))+"  "+desc)
	}
	if key := c.KeyFor("copy_mode"); key != "" {
		out = append(out, "",
			"copy mode ("+key+"):  hjkl w b e W B E 0 ^ $ gg G H M L { } f F t T ; ,",
			"  ctrl+u/d/f/b page   v V ctrl+v select   o swap   y/enter copy   esc leave")
	}
	if c.Mouse {
		out = append(out, "",
			"mouse: click = focus (in the overview: focus + center)",
			"mouse: drag inside a pane = select + copy (stays in that pane)",
			"mouse: wheel = scroll full-screen apps / switch workspace in overview")
	}
	return out
}
