//go:build windows

package governor

import (
	"context"
	"unsafe"

	"github.com/shirou/gopsutil/v4/cpu"
	"golang.org/x/sys/windows"
)

type winProbe struct{}

func newProbe() probe { return &winProbe{} }

// CPUPercent samples system-wide CPU use over a short window.
func (p *winProbe) CPUPercent(ctx context.Context) float64 {
	return sampleCPU(ctx)
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")

	user32                  = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procGetShellWindow      = user32.NewProc("GetShellWindow")
	procGetDesktopWindow    = user32.NewProc("GetDesktopWindow")
)

// systemPowerStatus mirrors the Win32 SYSTEM_POWER_STATUS structure.
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// Battery reads the power state from the Win32 power API.
func (p *winProbe) Battery() BatteryState {
	var status systemPowerStatus
	ret, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status)))
	if ret == 0 {
		return BatteryState{Percent: -1}
	}
	// BatteryFlag 128 means "no system battery", which is how desktops report.
	if status.BatteryFlag&128 != 0 {
		return BatteryState{Present: false, Charging: true, Percent: -1}
	}
	percent := -1
	if status.BatteryLifePercent != 255 {
		percent = int(status.BatteryLifePercent)
	}
	return BatteryState{
		Present:  true,
		Charging: status.ACLineStatus == 1,
		Percent:  percent,
	}
}

const (
	ifTypeWWANPP  = 243 // 3GPP mobile broadband
	ifTypeWWANPP2 = 244 // 3GPP2 mobile broadband
)

// Metered reports whether traffic is likely to be charged by the byte.
//
// Windows exposes a full cost API only through WinRT, which would pull a large
// dependency into a background agent. Detecting a mobile broadband adapter as
// the operational interface catches the case that actually costs users money -
// a phone hotspot over USB or a built-in LTE modem - and errs towards allowing
// backups when it cannot tell.
func (p *winProbe) Metered() bool {
	adapters, err := adapterAddresses()
	if err != nil {
		return false
	}
	for _, a := range adapters {
		if a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		if a.IfType == ifTypeWWANPP || a.IfType == ifTypeWWANPP2 {
			return true
		}
	}
	return false
}

// adapterAddresses wraps GetAdaptersAddresses, growing the buffer as needed.
func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	size := uint32(15 * 1024)
	for range 4 {
		buf := make([]byte, size)
		addr := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_UNICAST|windows.GAA_FLAG_SKIP_ANYCAST|
				windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, addr, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, err
		}
		var out []*windows.IpAdapterAddresses
		for cur := addr; cur != nil; cur = cur.Next {
			out = append(out, cur)
		}
		return out, nil
	}
	return nil, windows.ERROR_BUFFER_OVERFLOW
}

// Fullscreen reports whether the foreground window covers the whole screen,
// which is how games and full-screen video present themselves.
func (p *winProbe) Fullscreen() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	// The shell and desktop windows are always "fullscreen"; ignoring them
	// prevents pausing whenever the user is just on their desktop.
	if shell, _, _ := procGetShellWindow.Call(); shell == hwnd {
		return false
	}
	if desktop, _, _ := procGetDesktopWindow.Call(); desktop == hwnd {
		return false
	}

	var rect windows.Rect
	if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ret == 0 {
		return false
	}
	const smCXScreen, smCYScreen = 0, 1
	screenW, _, _ := procGetSystemMetrics.Call(smCXScreen)
	screenH, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if screenW == 0 || screenH == 0 {
		return false
	}
	width := int32(rect.Right - rect.Left)
	height := int32(rect.Bottom - rect.Top)
	return width >= int32(screenW) && height >= int32(screenH)
}

// sampleCPU is shared by the platform probes.
func sampleCPU(ctx context.Context) float64 {
	// A 300 ms sample is long enough to be meaningful and short enough that the
	// governor's own measurement is not itself a load.
	percents, err := cpu.PercentWithContext(ctx, 300_000_000, false)
	if err != nil || len(percents) == 0 {
		return -1
	}
	return percents[0]
}
