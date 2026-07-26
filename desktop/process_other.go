//go:build !windows

package main

import (
	"os"
	"syscall"
)

// processExists reports whether a process id belongs to a running process.
// Signal 0 performs the permission and existence checks without delivering
// anything.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
