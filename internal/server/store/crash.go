package store

import (
	"os"
	"time"
)

// crashWait pauses the process at stage when a crash test asks for it.
// The parent kills the process after the signal file appears. Both environment
// variables must be set, so a normal server never stops here. Killing the
// process is not a power cut: the operating system may still flush caches
// that a loss of power would not.
func crashWait(stage string) {
	if os.Getenv("OB_CRASH_AT") != stage || os.Getenv("OB_CRASH_SIGNAL") == "" {
		return
	}
	if err := os.WriteFile(os.Getenv("OB_CRASH_SIGNAL"), []byte(stage), 0o644); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(2)
	}
	for {
		time.Sleep(time.Hour)
	}
}
