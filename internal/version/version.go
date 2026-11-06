// Package version exposes build metadata for every OpenBackup binary.
package version

import (
	"fmt"
	"runtime"
)

// Values are injected at build time with -ldflags. When they are left at the
// dev default, init reads VERSION.md from a parent of the working directory
// so `go run` and `wails dev` inside this repository match the release file.
// A stamped build keeps the link-time value: the lookup only fills "dev".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func init() {
	if Version != "" && Version != "dev" {
		return
	}
	if v, ok := fallbackFromCwd(); ok {
		Version = v.Tag()
	}
}

// String returns a human readable one-line build description.
func String() string {
	return fmt.Sprintf("openbackup %s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// UserAgent returns the HTTP User-Agent used by agents.
func UserAgent() string {
	return fmt.Sprintf("openbackup-agent/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
