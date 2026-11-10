//go:build linux

package governor

import (
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func processCPU() time.Duration {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	text := string(raw)
	end := strings.LastIndex(text, ")")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 15 {
		return 0
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	// USER_HZ is 100 on every Linux architecture this agent runs on.
	return time.Duration(utime+stime) * time.Second / 100
}

func applyPriority(background bool) {
	if background {
		_ = unix.Setpriority(unix.PRIO_PROCESS, 0, 10)
		ioprio(3, 0) // idle
		return
	}
	_ = unix.Setpriority(unix.PRIO_PROCESS, 0, 0)
	ioprio(2, 4) // best-effort, default level
}

func ioprio(class, data int) {
	const whoProcess = 1
	prio := uintptr(class<<13 | data)
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, whoProcess, 0, prio)
}
