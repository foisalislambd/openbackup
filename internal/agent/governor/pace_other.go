//go:build !linux

package governor

import (
	"os"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func processCPU() time.Duration {
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return 0
	}
	times, err := p.Times()
	if err != nil || times == nil {
		return 0
	}
	return time.Duration((times.User + times.System) * float64(time.Second))
}
