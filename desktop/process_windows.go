package main

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// processExists reports whether a process id belongs to a running process.
//
// Windows has no signal 0, so the handle is opened and immediately closed. A
// handle that opens but whose process has exited still reports an exit code, so
// that case is checked too: otherwise a crashed window would leave a lock nobody
// could clear.
func processExists(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE
	return code == stillActive
}

// holderMatches reports whether pid belongs to a live OpenBackup executable,
// comparing the process image name against what the lock recorded. A bare PID
// check alone would let a recycled PID wedge startup forever.
func holderMatches(pid int, exeBase string) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE
	if code != stillActive {
		return false
	}

	buf := make([]uint16, 512)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return false
	}
	image := filepath.Base(windows.UTF16ToString(buf[:size]))
	return exeBase == "" || strings.EqualFold(image, exeBase)
}
