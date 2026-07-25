//go:build windows

package store

import "golang.org/x/sys/windows"

// freeDiskBytes reports the free space available on the volume holding path,
// or -1 when it cannot be determined. The dashboard shows it so a user notices
// a filling disk before backups start failing.
func freeDiskBytes(path string) int64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	var freeToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &free); err != nil {
		return -1
	}
	// freeToCaller respects per-user quotas, which is the number that actually
	// limits us.
	return int64(freeToCaller)
}
