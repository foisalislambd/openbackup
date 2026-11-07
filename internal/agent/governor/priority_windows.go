//go:build windows

package governor

import "golang.org/x/sys/windows"

func applyPriority(background bool) {
	proc := windows.CurrentProcess()
	if background {
		_ = windows.SetPriorityClass(proc, windows.BELOW_NORMAL_PRIORITY_CLASS)
		return
	}
	_ = windows.SetPriorityClass(proc, windows.NORMAL_PRIORITY_CLASS)
}
