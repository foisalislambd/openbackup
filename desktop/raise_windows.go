//go:build windows

package main

import "context"

// On Windows the tray icon is the usual way back into the app; a second launch
// simply exits. Raising via a socket is a Linux/macOS concern.
func listenForRaise(context.Context, func()) {}

func signalRaise() bool { return false }
