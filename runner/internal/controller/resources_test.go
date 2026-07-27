package controller

import (
	"testing"
)

func TestCollectResourceSnapshot(t *testing.T) {
	s := collectResourceSnapshot()

	// In a CI environment without /proc, all fields may be nil.
	// This test verifies the function doesn't panic.
	if s.CPUPercent != nil {
		cpu := *s.CPUPercent
		if cpu < 0 || cpu > 100 {
			t.Errorf("CPUPercent out of range: %f", cpu)
		}
	}
	if s.MemoryPercent != nil {
		mem := *s.MemoryPercent
		if mem < 0 || mem > 100 {
			t.Errorf("MemoryPercent out of range: %f", mem)
		}
	}
	if s.MemoryAvailableBytes != nil {
		avail := *s.MemoryAvailableBytes
		if avail < 0 {
			t.Errorf("MemoryAvailableBytes negative: %d", avail)
		}
	}
	if s.DiskPercent != nil {
		disk := *s.DiskPercent
		if disk < 0 || disk > 100 {
			t.Errorf("DiskPercent out of range: %f", disk)
		}
	}
}

func TestCPUPercentFromProc(t *testing.T) {
	cpu, err := cpuPercentFromProc()
	if err != nil {
		t.Skipf("no /proc/stat available: %v", err)
	}
	if cpu < 0 || cpu > 100 {
		t.Errorf("cpuPercentFromProc out of range: %f", cpu)
	}
}

func TestMemInfoFromProc(t *testing.T) {
	memPct, memAvail, err := memInfoFromProc()
	if err != nil {
		t.Skipf("no /proc/meminfo available: %v", err)
	}
	if memPct < 0 || memPct > 100 {
		t.Errorf("memInfoFromProc percent out of range: %f", memPct)
	}
	if memAvail < 0 {
		t.Errorf("memInfoFromProc available negative: %d", memAvail)
	}
}
