package governor

import (
	"context"
	"os"
	"runtime/debug"
	"time"
)

// Pace waits until the machine allows work and then, when an agent CPU budget
// is configured, sleeps so this process stays near that fraction of one core.
// It is meant to be called between files and between chunk batches.
func (g *Governor) Pace(ctx context.Context) error {
	if _, err := g.WaitUntilAllowed(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	limit := g.limits.AgentCPUPercent
	g.mu.Unlock()
	if limit <= 0 || limit >= 100 {
		return nil
	}
	nowCPU := processCPU()
	now := time.Now()
	g.mu.Lock()
	prevCPU := g.prevCPU
	prevWall := g.prevWall
	g.prevCPU = nowCPU
	g.prevWall = now
	g.mu.Unlock()
	if prevWall.IsZero() || nowCPU < prevCPU {
		return nil
	}
	wall := now.Sub(prevWall)
	if wall < 20*time.Millisecond {
		return nil
	}
	used := nowCPU - prevCPU
	budget := time.Duration(float64(wall) * limit / 100)
	if used <= budget {
		return nil
	}
	timer := time.NewTimer(used - budget)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ApplyMemoryLimit installs GOMEMLIMIT for this process. Zero leaves the
// existing limit alone. The Go runtime already honours the GOMEMLIMIT
// environment variable; this is the configuration-file equivalent.
func ApplyMemoryLimit(bytes int64) {
	if bytes <= 0 {
		return
	}
	debug.SetMemoryLimit(bytes)
}

// ApplyPriority lowers or restores the process CPU and IO priority.
// "normal" keeps the defaults. Anything else, including empty, requests
// background scheduling. Failures are ignored: a backup must still run when
// the platform refuses the call.
func ApplyPriority(priority string) {
	applyPriority(priority != "normal" && priority != "foreground")
}

// processCPU is implemented per OS. A zero result disables pacing for that call.
var _ = os.Getpid
