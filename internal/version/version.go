// Package version exposes build metadata for every OpenBackup binary.
package version

import (
	"fmt"
	"runtime"
)

// Values are injected at build time with -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human readable one-line build description.
func String() string {
	return fmt.Sprintf("openbackup %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// UserAgent returns the HTTP User-Agent used by agents.
func UserAgent() string {
	return fmt.Sprintf("openbackup-agent/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
