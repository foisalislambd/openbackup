package main

// Windows draws the notification area from an ICO, and a PNG there renders as a
// blank square rather than failing loudly.
var trayExtensions = []string{".ico", ".png"}
