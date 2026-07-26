//go:build !windows

package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
)

// raiseSocketPath is where the running window listens for "please show yourself"
// from a second launch. Unix domain sockets work on Linux and macOS.
func raiseSocketPath() (string, error) {
	stateDir, err := config.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "desktop.raise.sock"), nil
}

// listenForRaise starts a socket that shows the window when a second copy of the
// app is launched. It stops when ctx is cancelled.
func listenForRaise(ctx context.Context, raise func()) {
	path, err := raiseSocketPath()
	if err != nil {
		return
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(path)
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			line, _ := bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
			if line != "" {
				raise()
			}
		}
	}()
}

// signalRaise asks the already-running window to come to the front.
func signalRaise() bool {
	path, err := raiseSocketPath()
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write([]byte("raise\n"))
	return err == nil
}
