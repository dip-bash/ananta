package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestResolveSession(t *testing.T) {
	cases := []struct {
		name                      string
		explicit, session, inside string
		want                      string
		fromEnv, wantErr          bool
	}{
		{"default outside ananta", "", "", "", "main", false, false},
		{"explicit wins outside", "test", "", "", "test", false, false},
		{"inside a session pane", "", "work", "1", "work", true, false},
		{"explicit beats the current session", "test", "work", "1", "test", false, false},
		{"inside local mode: no session to talk to", "", "", "1", "", false, true},
		{"inside local mode but explicit", "test", "", "1", "test", false, false},
	}
	for _, c := range cases {
		t.Setenv("ANANTA_SESSION", c.session)
		t.Setenv("ANANTA", c.inside)
		got, fromEnv, err := resolveSession(c.explicit)
		if got != c.want || fromEnv != c.fromEnv || (err != nil) != c.wantErr {
			t.Errorf("%s: got (%q, %v, %v), want (%q, %v, err=%v)", c.name, got, fromEnv, err, c.want, c.fromEnv, c.wantErr)
		}
	}
}

func TestFlagsMayComeAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"htop", "-n", "3", "-w", "33%"},
		{"-n", "3", "htop", "-w", "33%"},
		{"-n=3", "-w=33%", "htop"},
		{"--workspace", "3", "--width", "33%", "htop"},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var n int
		var w string
		fs.IntVar(&n, "n", 0, "")
		fs.IntVar(&n, "workspace", 0, "")
		fs.StringVar(&w, "w", "", "")
		fs.StringVar(&w, "width", "", "")
		pos, err := parseInterleaved(fs, args)
		if err != nil || n != 3 || w != "33%" || !reflect.DeepEqual(pos, []string{"htop"}) {
			t.Errorf("%v: n=%d w=%q pos=%v err=%v", args, n, w, pos, err)
		}
	}
}

func TestDoubleDashMakesEverythingPositional(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var n int
	fs.IntVar(&n, "n", 0, "")
	pos, err := parseInterleaved(fs, []string{"-n", "2", "--", "-n", "x"})
	if err != nil || n != 2 || !reflect.DeepEqual(pos, []string{"-n", "x"}) {
		t.Errorf("n=%d pos=%v err=%v", n, pos, err)
	}
}
