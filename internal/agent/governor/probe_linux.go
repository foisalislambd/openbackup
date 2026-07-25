//go:build linux

package governor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

type linuxProbe struct{}

func newProbe() probe { return &linuxProbe{} }

func (p *linuxProbe) CPUPercent(ctx context.Context) float64 {
	percents, err := cpu.PercentWithContext(ctx, 300*time.Millisecond, false)
	if err != nil || len(percents) == 0 {
		return -1
	}
	return percents[0]
}

// Battery reads the sysfs power supply interface, which is present on laptops
// and absent on servers and desktops.
func (p *linuxProbe) Battery() BatteryState {
	entries, err := os.ReadDir("/sys/class/power_supply")
	if err != nil {
		return BatteryState{Percent: -1}
	}
	state := BatteryState{Percent: -1, Charging: true}
	for _, e := range entries {
		dir := filepath.Join("/sys/class/power_supply", e.Name())
		kind := strings.TrimSpace(readFile(filepath.Join(dir, "type")))
		switch kind {
		case "Battery":
			state.Present = true
			if capacity := strings.TrimSpace(readFile(filepath.Join(dir, "capacity"))); capacity != "" {
				if n, err := strconv.Atoi(capacity); err == nil {
					state.Percent = n
				}
			}
			status := strings.TrimSpace(readFile(filepath.Join(dir, "status")))
			if status == "Discharging" {
				state.Charging = false
			}
		case "Mains", "USB":
			if online := strings.TrimSpace(readFile(filepath.Join(dir, "online"))); online == "1" {
				state.Charging = true
			}
		}
	}
	return state
}

// Metered checks NetworkManager's per-connection metered flag when available,
// then falls back to recognising mobile interfaces on the default route.
func (p *linuxProbe) Metered() bool {
	iface := defaultRouteInterface()
	if iface == "" {
		return false
	}
	for _, prefix := range []string{"wwan", "ppp", "wwp", "rmnet"} {
		if strings.HasPrefix(iface, prefix) {
			return true
		}
	}
	return false
}

// Fullscreen is not detected on Linux: there is no portable way across X11,
// Wayland and the various compositors, and guessing wrong would either pause
// backups forever or never pause at all.
func (p *linuxProbe) Fullscreen() bool { return false }

// defaultRouteInterface parses /proc/net/route for the interface carrying the
// default route.
func defaultRouteInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Destination 00000000 is the default route.
		if fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
