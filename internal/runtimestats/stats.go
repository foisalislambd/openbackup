// Package runtimestats writes a process's Go allocation counters on exit.
//
// It exists for the reproducible benchmark (scripts/bench). Production runs
// leave OPENBACKUP_RUNTIME_STATS unset and pay nothing beyond one environment
// lookup at startup.
package runtimestats

import (
	"encoding/json"
	"os"
	"runtime"
)

// Snapshot is the subset of runtime.MemStats a benchmark can compare.
type Snapshot struct {
	TotalAlloc uint64 `json:"total_alloc"`
	Mallocs    uint64 `json:"mallocs"`
	Frees      uint64 `json:"frees"`
	NumGC      uint32 `json:"num_gc"`
	PauseTotal uint64 `json:"pause_total_ns"`
	HeapAlloc  uint64 `json:"heap_alloc"`
	HeapInuse  uint64 `json:"heap_inuse"`
	Sys        uint64 `json:"sys"`
	// RSSKiB and HWMKiB are process resident memory at exit. HWM is the peak
	// since the process started. Both are zero when /proc is unavailable.
	RSSKiB uint64 `json:"rss_kib"`
	HWMKiB uint64 `json:"hwm_kib"`
}

// Dump writes MemStats as JSON to path. An empty path does nothing.
// Errors are ignored: a benchmark helper must not change the process exit code.
func Dump(path string) {
	if path == "" {
		return
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	rss, hwm := readRSS()
	snap := Snapshot{
		TotalAlloc: m.TotalAlloc,
		Mallocs:    m.Mallocs,
		Frees:      m.Frees,
		NumGC:      m.NumGC,
		PauseTotal: m.PauseTotalNs,
		HeapAlloc:  m.HeapAlloc,
		HeapInuse:  m.HeapInuse,
		Sys:        m.Sys,
		RSSKiB:     rss,
		HWMKiB:     hwm,
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o644)
}
