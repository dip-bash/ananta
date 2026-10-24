package ananta

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type statusMetrics struct {
	CPU     string
	Memory  string
	Load    string
	Battery string
}

type statusSampler struct {
	mu        sync.RWMutex
	data      statusMetrics
	done      chan struct{}
	lastTotal uint64
	lastIdle  uint64
}

func newStatusSampler(notify func()) *statusSampler {
	s := &statusSampler{done: make(chan struct{})}
	s.refresh()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.refresh()
				if notify != nil {
					notify()
				}
			case <-s.done:
				return
			}
		}
	}()
	return s
}

func (s *statusSampler) Close() { close(s.done) }

func (s *statusSampler) Snapshot() statusMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *statusSampler) refresh() {
	d := statusMetrics{CPU: "cpu: --", Memory: "mem: --", Load: "load: --", Battery: "bat: --"}
	if runtime.GOOS == "linux" {
		if cpu := s.readCPU(); cpu != "" {
			d.CPU = cpu
		}
		if mem := readLinuxMemory(); mem != "" {
			d.Memory = mem
		}
		if load := readLinuxLoad(); load != "" {
			d.Load = load
		}
		if bat := readLinuxBattery(); bat != "" {
			d.Battery = bat
		}
	}
	s.mu.Lock()
	s.data = d
	s.mu.Unlock()
}

func (s *statusSampler) readCPU() string {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return ""
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return ""
	}
	vals := make([]uint64, 0, len(fields)-1)
	for _, p := range fields[1:] {
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return ""
		}
		vals = append(vals, v)
	}
	if len(vals) < 4 {
		return ""
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	if s.lastTotal == 0 || total <= s.lastTotal || idle < s.lastIdle {
		s.lastTotal, s.lastIdle = total, idle
		return ""
	}
	dt := total - s.lastTotal
	di := idle - s.lastIdle
	s.lastTotal, s.lastIdle = total, idle
	if dt == 0 {
		return ""
	}
	used := 100 * float64(dt-di) / float64(dt)
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return fmt.Sprintf("cpu: %.0f%%", used)
}

func readLinuxMemory() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	var total, avail uint64
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, _ = strconv.ParseUint(f[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseUint(f[1], 10, 64)
		}
	}
	if total == 0 || avail > total {
		return ""
	}
	usedPct := 100 * float64(total-avail) / float64(total)
	return fmt.Sprintf("mem: %.0f%%", usedPct)
}

func readLinuxLoad() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return ""
	}
	return "load: " + fields[0]
}

func readLinuxBattery() string {
	entries, err := os.ReadDir("/sys/class/power_supply")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "BAT") {
			continue
		}
		capData, err := os.ReadFile(filepath.Join("/sys/class/power_supply", name, "capacity"))
		if err != nil {
			continue
		}
		cap := strings.TrimSpace(string(capData))
		statusData, _ := os.ReadFile(filepath.Join("/sys/class/power_supply", name, "status"))
		st := strings.TrimSpace(strings.ToLower(string(statusData)))
		icon := ""
		switch st {
		case "charging":
			icon = "+"
		case "discharging":
			icon = "-"
		}
		return "bat: " + cap + "%" + icon
	}
	return ""
}
