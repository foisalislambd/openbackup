//go:build !linux

package runtimestats

func readRSS() (rss, hwm uint64) { return 0, 0 }
