//go:build !windows

package store

import "golang.org/x/sys/unix"

// freeDiskBytes reports the free space available on the filesystem holding path,
// or -1 when it cannot be determined.
func freeDiskBytes(path string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return -1
	}
	// Bavail excludes blocks reserved for root, so it is what a non-root server
	// can really use.
	return int64(st.Bavail) * int64(st.Bsize)
}
