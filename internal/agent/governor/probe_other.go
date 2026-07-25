//go:build !windows && !linux

package governor

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

type genericProbe struct{}

func newProbe() probe { return &genericProbe{} }

func (p *genericProbe) CPUPercent(ctx context.Context) float64 {
	percents, err := cpu.PercentWithContext(ctx, 300*time.Millisecond, false)
	if err != nil || len(percents) == 0 {
		return -1
	}
	return percents[0]
}

// Battery shells out to pmset on macOS. Reading the power source properly needs
// IOKit through cgo, and a cgo-free binary that cross-compiles is worth more
// than avoiding one short-lived subprocess every few seconds of active backup.
func (p *genericProbe) Battery() BatteryState {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return BatteryState{Percent: -1}
	}
	text := string(out)
	state := BatteryState{Percent: -1}
	if strings.Contains(text, "InternalBattery") {
		state.Present = true
	} else {
		// No internal battery: a Mac mini or Mac Pro is always on mains.
		return BatteryState{Present: false, Charging: true, Percent: -1}
	}
	state.Charging = strings.Contains(text, "'AC Power'") ||
		strings.Contains(text, "AC attached") || strings.Contains(text, "charging")
	if idx := strings.Index(text, "%"); idx > 0 {
		start := idx - 1
		for start > 0 && text[start-1] >= '0' && text[start-1] <= '9' {
			start--
		}
		if n, err := strconv.Atoi(text[start:idx]); err == nil {
			state.Percent = n
		}
	}
	return state
}

// Metered is not detected on these platforms; macOS exposes "low data mode"
// only through Network.framework.
func (p *genericProbe) Metered() bool { return false }

// Fullscreen is not detected on these platforms.
func (p *genericProbe) Fullscreen() bool { return false }
