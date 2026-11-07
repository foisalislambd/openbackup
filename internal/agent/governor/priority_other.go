//go:build !linux && !windows && !darwin && !freebsd && !netbsd && !openbsd

package governor

func applyPriority(bool) {}
