// Package stats samples system information for the status bar (CPU, memory,
// network speed, battery, load) and runs user-defined command widgets.
//
// Everything is read from /proc and /sys, so on platforms without them
// (macOS, restricted Android) the built-in widgets simply report "unknown"
// and the bar hides them. Custom command widgets work everywhere.
package stats

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot is a point-in-time copy of all values. Negative / zero values mean
// "unknown".
type Snapshot struct {
	CPU      float64 // percent busy, <0 unknown
	MemUsed  uint64  // bytes
	MemTotal uint64  // bytes, 0 unknown
	NetDown  float64 // bytes/sec, <0 unknown
	NetUp    float64
	Battery  int // percent, <0 no battery
	Charging bool
	Load1    float64
	LoadOK   bool
	Custom   map[string]string
}

// Custom describes a user command widget.
type Custom struct {
	Command  string
	Interval time.Duration
}

type Collector struct {
	mu   sync.RWMutex
	snap Snapshot
	done chan struct{}
	once sync.Once

	prevIdle, prevTotal uint64
	prevRX, prevTX      uint64
	havePrev            bool
	havePrevNet         bool
	prevAt              time.Time
}

func New() *Collector {
	return &Collector{
		done: make(chan struct{}),
		snap: Snapshot{CPU: -1, NetDown: -1, NetUp: -1, Battery: -1, Custom: map[string]string{}},
	}
}

// Start begins sampling every interval and running the custom widgets.
// notify is called (from other goroutines) whenever a value changed.
func (c *Collector) Start(interval time.Duration, custom map[string]Custom, notify func()) {
	if interval < 200*time.Millisecond {
		interval = time.Second
	}
	c.sample()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.sample()
				notify()
			case <-c.done:
				return
			}
		}
	}()
	for name, spec := range custom {
		go c.runCustom(name, spec, notify)
	}
}

func (c *Collector) Stop() { c.once.Do(func() { close(c.done) }) }

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.snap
	s.Custom = make(map[string]string, len(c.snap.Custom))
	for k, v := range c.snap.Custom {
		s.Custom[k] = v
	}
	return s
}

func (c *Collector) runCustom(name string, spec Custom, notify func()) {
	iv := spec.Interval
	if iv < time.Second {
		iv = 5 * time.Second
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), iv)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", spec.Command).Output()
		text := ""
		if err == nil {
			text = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
			if r := []rune(text); len(r) > 60 {
				text = string(r[:60])
			}
		}
		c.mu.Lock()
		c.snap.Custom[name] = text
		c.mu.Unlock()
		notify()
	}
	run()
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			run()
		case <-c.done:
			return
		}
	}
}

func (c *Collector) sample() {
	now := time.Now()
	s := Snapshot{CPU: -1, NetDown: -1, NetUp: -1, Battery: -1}

	c.mu.RLock()
	s.CPU, s.NetDown, s.NetUp = c.snap.CPU, c.snap.NetDown, c.snap.NetUp
	c.mu.RUnlock()

	if idle, total, ok := readCPU(); ok {
		if c.havePrev && total > c.prevTotal {
			dt := float64(total - c.prevTotal)
			di := float64(idle - c.prevIdle)
			s.CPU = 100 * (1 - di/dt)
			if s.CPU < 0 {
				s.CPU = 0
			}
		}
		c.prevIdle, c.prevTotal, c.havePrev = idle, total, true
	}
	s.MemUsed, s.MemTotal = readMem()
	if rx, tx, ok := readNet(); ok {
		if c.havePrevNet {
			if dt := now.Sub(c.prevAt).Seconds(); dt > 0 && rx >= c.prevRX && tx >= c.prevTX {
				s.NetDown = float64(rx-c.prevRX) / dt
				s.NetUp = float64(tx-c.prevTX) / dt
			}
		}
		c.prevRX, c.prevTX, c.havePrevNet, c.prevAt = rx, tx, true, now
	}
	s.Battery, s.Charging = readBattery()
	s.Load1, s.LoadOK = readLoad()

	c.mu.Lock()
	s.Custom = c.snap.Custom
	c.snap = s
	c.mu.Unlock()
}

func readCPU() (idle, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return
	}
	for i, s := range f[1:] {
		if i >= 8 {
			break
		}
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	return idle, total, total > 0
}

func readMem() (used, total uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	var avail uint64
	have := false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail, have = v*1024, true
		}
	}
	if total == 0 || !have {
		return 0, total
	}
	return total - avail, total
}

func readNet() (rx, tx uint64, ok bool) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx, tx, ok = rx+r, tx+t, true
	}
	return
}

func readBattery() (pct int, charging bool) {
	pct = -1
	entries, err := os.ReadDir("/sys/class/power_supply")
	if err != nil {
		return
	}
	for _, e := range entries {
		dir := "/sys/class/power_supply/" + e.Name() + "/"
		if t, err := os.ReadFile(dir + "type"); err != nil || strings.TrimSpace(string(t)) != "Battery" {
			continue
		}
		b, err := os.ReadFile(dir + "capacity")
		if err != nil {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		st, _ := os.ReadFile(dir + "status")
		s := strings.TrimSpace(string(st))
		return v, s == "Charging" || s == "Full"
	}
	return
}

func readLoad() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil
}
