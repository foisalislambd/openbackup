//go:build !windows

package main

// macOS and the Linux desktops take a PNG.
var trayExtensions = []string{".png", ".ico"}
