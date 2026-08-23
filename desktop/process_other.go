//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// holderMatches reports whether pid belongs to a live OpenBackup executable.
// On Linux the resolved binary is readable through /proc; where that does not
// exist (macOS) we fall back to plain liveness, as before.
func holderMatches(pid int, exeBase string) bool {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return processExists(pid)
	}
	return exeBase == "" || strings.EqualFold(filepath.Base(link), exeBase)
}
