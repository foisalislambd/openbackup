// Package governor decides when the agent is allowed to work.
//
// This is what makes the agent invisible. A backup tool that uploads while the
// user is gaming, on a mobile hotspot, or on 8% battery gets blamed for every
// stutter and every overage, and then it gets uninstalled. The rules here are
// deliberately cautious: when in doubt about the machine's state, keep working
// slowly rather than stopping (a paused backup that never resumes is a silent
// failure), but stop immediately for the cases we can measure confidently.
package governor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openbackup/openbackup/internal/agent/config"
)

// State is the governor's verdict.
type State struct {
	// Allowed reports whether backup work may proceed.
	Allowed bool
	// Reason explains a pause in words a user can act on.
	Reason string
	// Snapshot of the measurements behind the decision.
	CPUPercent float64
	Battery    BatteryState
	Metered    bool
	Fullscreen bool
}

// BatteryState describes the power source.
type BatteryState struct {
	// Present reports whether a battery was detected.
	Present bool
	// Charging reports whether it is plugged in.
	Charging bool
	// Percent is the charge level, or -1 when unknown.
	Percent int
}

// probe reads the machine's state. Implementations are per-platform, and any
// value they cannot determine is reported as unknown rather than guessed.
type probe interface {
	// CPUPercent returns system-wide CPU utilisation over the sampling window,
	// or -1 when unknown.
	CPUPercent(ctx context.Context) float64
	// Battery reports the power source.
	Battery() BatteryState
	// Metered reports whether the active connection is likely to be metered.
	Metered() bool
	// Fullscreen reports whether a fullscreen application is in the foreground,
	// which is the practical way to detect games and presentations.
	Fullscreen() bool
}

// Governor evaluates whether work may proceed.
type Governor struct {
	limits config.Limits
	probe  probe

	mu     sync.Mutex
	last   State
	lastAt time.Time
	// manualPause is set by the user or by a server command.
	manualPause bool
	pauseUntil  time.Time
}

// cacheTTL is how long a verdict is reused. Probing costs a little CPU itself,
// so re-measuring per file would defeat the purpose.
const cacheTTL = 5 * time.Second

// New builds a Governor.
func New(limits config.Limits) *Governor {
	return &Governor{limits: limits, probe: newProbe()}
}

// SetLimits updates the thresholds, for example after the server pushes a policy.
func (g *Governor) SetLimits(limits config.Limits) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limits = limits
	g.lastAt = time.Time{}
}

// Pause stops work until Resume is called.
func (g *Governor) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manualPause = true
}

// PauseFor stops work for a period, used for "snooze for an hour".
func (g *Governor) PauseFor(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pauseUntil = time.Now().Add(d)
}

// Resume clears a manual pause.
func (g *Governor) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manualPause = false
	g.pauseUntil = time.Time{}
}

// Paused reports whether a manual pause is in effect.
func (g *Governor) Paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.manualPause || time.Now().Before(g.pauseUntil)
}

// Evaluate returns the current verdict, cached briefly.
func (g *Governor) Evaluate(ctx context.Context) State {
	g.mu.Lock()
	if time.Since(g.lastAt) < cacheTTL && !g.last.isZero() {
		state := g.last
		manual := g.manualPause || time.Now().Before(g.pauseUntil)
		g.mu.Unlock()
		if manual {
			return State{Allowed: false, Reason: "paused by you"}
		}
		return state
	}
	limits := g.limits
	manual := g.manualPause
	pauseUntil := g.pauseUntil
	g.mu.Unlock()

	if manual {
		return State{Allowed: false, Reason: "paused by you"}
	}
	if time.Now().Before(pauseUntil) {
		return State{
			Allowed: false,
			Reason:  fmt.Sprintf("snoozed for another %s", time.Until(pauseUntil).Round(time.Minute)),
		}
	}

	state := State{Allowed: true}
	state.CPUPercent = g.probe.CPUPercent(ctx)
	state.Battery = g.probe.Battery()
	state.Metered = g.probe.Metered()
	state.Fullscreen = g.probe.Fullscreen()

	switch {
	case limits.PauseWhileFullscreen && state.Fullscreen:
		state.Allowed = false
		state.Reason = "a fullscreen app is running (game or presentation)"

	case limits.PauseOnMetered && state.Metered:
		state.Allowed = false
		state.Reason = "the network connection looks metered"

	case state.Battery.Present && !state.Battery.Charging && limits.PauseOnBattery:
		state.Allowed = false
		state.Reason = "running on battery"

	case state.Battery.Present && !state.Battery.Charging &&
		state.Battery.Percent >= 0 && limits.MinBatteryPercent > 0 &&
		state.Battery.Percent < limits.MinBatteryPercent:
		state.Allowed = false
		state.Reason = fmt.Sprintf("battery is at %d%%, below the %d%% floor",
			state.Battery.Percent, limits.MinBatteryPercent)

	case limits.MaxCPUPercent > 0 && state.CPUPercent >= 0 && state.CPUPercent > limits.MaxCPUPercent:
		state.Allowed = false
		state.Reason = fmt.Sprintf("the machine is busy (%.0f%% CPU)", state.CPUPercent)
	}

	g.mu.Lock()
	g.last = state
	g.lastAt = time.Now()
	g.mu.Unlock()
	return state
}

// WaitUntilAllowed blocks until work is permitted or ctx is done. It returns the
// reason it had to wait, so the caller can report a state change once rather
// than logging on every poll.
func (g *Governor) WaitUntilAllowed(ctx context.Context) (waitedFor string, err error) {
	state := g.Evaluate(ctx)
	if state.Allowed {
		return "", nil
	}
	reason := state.Reason
	ticker := time.NewTicker(cacheTTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return reason, ctx.Err()
		case <-ticker.C:
			if g.Evaluate(ctx).Allowed {
				return reason, nil
			}
		}
	}
}

func (s State) isZero() bool { return s.Reason == "" && !s.Allowed && s.CPUPercent == 0 }

// Describe renders the state for the CLI and the tray tooltip.
func (s State) Describe() string {
	if s.Allowed {
		if s.CPUPercent >= 0 {
			return fmt.Sprintf("running (CPU %.0f%%)", s.CPUPercent)
		}
		return "running"
	}
	return "paused: " + s.Reason
}
