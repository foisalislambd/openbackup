//go:build unix

package scanner

import (
	"os"
	"syscall"
)

func fileIdent(info os.FileInfo) uint64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0
	}
	return uint64(st.Ino)
}
