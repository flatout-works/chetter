//go:build windows

package controller

// collectResourceSnapshot returns an empty snapshot on Windows — the runner
// containers run on Linux.
func collectResourceSnapshot() resourceSnapshot {
	return resourceSnapshot{}
}
