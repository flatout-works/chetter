//go:build !windows

package controller

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// resourceSnapshot holds collected system resource metrics. Fields may be nil
// when the metric could not be collected (containerized environment, missing
// procfs, etc.). Callers should check for nil before use.
type resourceSnapshot struct {
	CPUPercent           *float64
	MemoryPercent        *float64
	MemoryAvailableBytes *int64
	DiskPercent          *float64
}

// collectResourceSnapshot reads /proc/stat, /proc/meminfo, and performs a
// statfs on the root filesystem to compute resource utilization. Any metric
// that cannot be collected is left nil.
func collectResourceSnapshot() resourceSnapshot {
	var s resourceSnapshot

	if cpu, err := cpuPercentFromProc(); err == nil {
		s.CPUPercent = &cpu
	}
	if memPct, memAvail, err := memInfoFromProc(); err == nil {
		pct := memPct
		avail := memAvail
		s.MemoryPercent = &pct
		s.MemoryAvailableBytes = &avail
	}
	if disk, err := diskPercentFromStatfs(); err == nil {
		s.DiskPercent = &disk
	}
	return s
}

// cpuPercentFromProc reads /proc/stat once and returns an instantaneous CPU
// utilization percentage (0–100). The value represents the non-idle fraction
// across all CPUs since boot — the caller should treat it as a point-in-time
// snapshot for relative comparison.
func cpuPercentFromProc() (float64, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, fmt.Errorf("empty /proc/stat")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, fmt.Errorf("unexpected /proc/stat format")
	}
	// fields: cpu user nice system idle iowait irq softirq steal ...
	var total, idle float64
	for i, field := range fields[1:] {
		v, err := strconv.ParseFloat(field, 64)
		if err != nil {
			continue
		}
		total += v
		// idle (index 3) and iowait (index 4) count as idle time.
		if i == 3 || i == 4 {
			idle += v
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("zero total CPU time")
	}
	pct := ((total - idle) / total) * 100.0
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct, nil
}

// memInfoFromProc reads /proc/meminfo and returns memory usage percentage
// (0–100) and available memory in bytes. Uses MemAvailable when present
// (Linux 3.14+), falling back to MemFree + Buffers + Cached.
func memInfoFromProc() (float64, int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var memTotal, memAvailable, memFree, buffers, cached int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo values are in kB.
		switch key {
		case "MemTotal":
			memTotal = value * 1024
		case "MemAvailable":
			memAvailable = value * 1024
		case "MemFree":
			memFree = value * 1024
		case "Buffers":
			buffers = value * 1024
		case "Cached":
			cached = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if memTotal == 0 {
		return 0, 0, fmt.Errorf("zero MemTotal in /proc/meminfo")
	}
	if memAvailable == 0 {
		memAvailable = memFree + buffers + cached
	}
	used := memTotal - memAvailable
	if used < 0 {
		used = 0
	}
	pct := float64(used) / float64(memTotal) * 100.0
	if pct > 100 {
		pct = 100
	}
	return pct, memAvailable, nil
}

// diskPercentFromStatfs uses statfs(2) on the root filesystem ("/") to
// compute disk usage percentage. Returns an error when statfs fails (e.g. in
// containerized environments where the rootfs is not a real block device).
func diskPercentFromStatfs() (float64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return 0, err
	}
	if stat.Blocks == 0 {
		return 0, fmt.Errorf("zero blocks on /")
	}
	used := stat.Blocks - stat.Bfree
	pct := float64(used) / float64(stat.Blocks) * 100.0
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct, nil
}
