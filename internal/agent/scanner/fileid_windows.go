//go:build windows

package scanner

import (
	"os"
	"syscall"
)

func fileIdent(info os.FileInfo) uint64 {
	st, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || st == nil {
		return 0
	}
	// Win32FileAttributeData does not carry the file index. Callers that need
	// it use a zero inode, and the size+mtime check still applies.
	return 0
}
