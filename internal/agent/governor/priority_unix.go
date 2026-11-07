//go:build darwin || freebsd || netbsd || openbsd

package governor

import "golang.org/x/sys/unix"

func applyPriority(background bool) {
	nice := 0
	if background {
		nice = 10
	}
	_ = unix.Setpriority(unix.PRIO_PROCESS, 0, nice)
}
