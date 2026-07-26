package main

import "golang.org/x/sys/windows"

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
