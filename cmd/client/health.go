package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Health metrics are read straight from /proc and /sys — no subprocesses, and
// every probe degrades to -1 ("unknown") when the kernel interface is missing,
// so this works on any Linux, not just Arch/KDE.
const metricUnavailable = -1.0

type healthSample struct {
	CPUPct    float64
	MemPct    float64
	DiskPct   float64
	TempC     float64
	Load1     float64
	UptimeSec int64
}

// cpuTimes is one snapshot of aggregate CPU jiffies.
type cpuTimes struct {
	idle  uint64
	total uint64
	ok    bool
}

var lastCPU cpuTimes

func collectHealth() healthSample {
	return healthSample{
		CPUPct:    cpuPercent(),
		MemPct:    memPercent(),
		DiskPct:   diskPercent("/"),
		TempC:     cpuTemperature(),
		Load1:     loadAvg1(),
		UptimeSec: uptimeSeconds(),
	}
}

// cpuPercent computes busy time since the previous call. The first call after
// start has no baseline and reports unavailable.
func cpuPercent() float64 {
	cur, ok := readCPUTimes()
	if !ok {
		return metricUnavailable
	}
	prev := lastCPU
	lastCPU = cur
	if !prev.ok || cur.total <= prev.total {
		return metricUnavailable
	}
	totalDelta := float64(cur.total - prev.total)
	idleDelta := float64(cur.idle - prev.idle)
	pct := (1 - idleDelta/totalDelta) * 100
	return clampPct(pct)
}

func readCPUTimes() (cpuTimes, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var total, idle uint64
		for i, raw := range fields[1:] {
			v, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				continue
			}
			total += v
			// fields[4] = idle, fields[5] = iowait
			if i == 3 || i == 4 {
				idle += v
			}
		}
		return cpuTimes{idle: idle, total: total, ok: true}, true
	}
	return cpuTimes{}, false
}

func memPercent() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return metricUnavailable
	}
	defer f.Close()
	var total, available float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMeminfoKB(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			available = parseMeminfoKB(line)
		}
		if total > 0 && available > 0 {
			break
		}
	}
	if total <= 0 {
		return metricUnavailable
	}
	return clampPct((total - available) / total * 100)
}

func parseMeminfoKB(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0
	}
	return v
}

func diskPercent(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return metricUnavailable
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	if total <= 0 {
		return metricUnavailable
	}
	free := float64(st.Bavail) * float64(st.Bsize)
	return clampPct((total - free) / total * 100)
}

// cpuTemperature picks the hottest thermal zone that looks like a CPU sensor,
// falling back to any zone. Machines without thermal zones report unavailable.
func cpuTemperature() float64 {
	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil || len(zones) == 0 {
		return hwmonTemperature()
	}
	best := metricUnavailable
	bestPreferred := false
	for _, zone := range zones {
		raw, err := os.ReadFile(filepath.Join(zone, "temp"))
		if err != nil {
			continue
		}
		milli, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil {
			continue
		}
		c := milli / 1000
		if c <= 0 || c > 150 {
			continue
		}
		typeRaw, _ := os.ReadFile(filepath.Join(zone, "type"))
		zoneType := strings.ToLower(strings.TrimSpace(string(typeRaw)))
		preferred := strings.Contains(zoneType, "cpu") ||
			strings.Contains(zoneType, "pkg") ||
			strings.Contains(zoneType, "x86")
		if preferred && (!bestPreferred || c > best) {
			best, bestPreferred = c, true
			continue
		}
		if !bestPreferred && c > best {
			best = c
		}
	}
	if best == metricUnavailable {
		return hwmonTemperature()
	}
	return best
}

// hwmonTemperature is the fallback for systems exposing sensors only via hwmon.
func hwmonTemperature() float64 {
	files, err := filepath.Glob("/sys/class/hwmon/hwmon*/temp1_input")
	if err != nil {
		return metricUnavailable
	}
	best := metricUnavailable
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		milli, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil {
			continue
		}
		c := milli / 1000
		if c > 0 && c <= 150 && c > best {
			best = c
		}
	}
	return best
}

func loadAvg1() float64 {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return metricUnavailable
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return metricUnavailable
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return metricUnavailable
	}
	return v
}

func uptimeSeconds() int64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(v)
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	// One decimal is plenty and keeps the JSON small.
	return float64(int(v*10+0.5)) / 10
}

// healthInterval is how often the client reports metrics. Deliberately slower
// than the 15s heartbeat so the fleet doesn't hammer the DB.
const healthInterval = 45 * time.Second
