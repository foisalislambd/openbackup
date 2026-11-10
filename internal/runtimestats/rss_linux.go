package runtimestats

import (
	"os"
	"strconv"
	"strings"
)

func readRSS() (rss, hwm uint64) {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "VmRSS":
			rss = n
		case "VmHWM":
			hwm = n
		}
	}
	return rss, hwm
}
