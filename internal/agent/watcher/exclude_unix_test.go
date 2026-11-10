//go:build unix

package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/ignore"
)

func TestExcludedRootIsNotListed(t *testing.T) {
	root := t.TempDir()
	excluded := filepath.Join(root, "node_modules")
	if err := os.Mkdir(excluded, 0o755); err != nil {
		t.Fatal(err)
	}
	// Opening this fifo blocks forever. AddRoots must return without reading it.
	if err := syscall.Mkfifo(filepath.Join(excluded, ".openbackupignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(ignore.New(ignore.Config{}), time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	done := make(chan error, 1)
	go func() {
		done <- w.AddRoots(context.Background(), []config.Root{{
			Name: "proj", Path: root, Enabled: true,
		}})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("AddRoots blocked on an excluded directory")
	}
	for _, dir := range w.watchList() {
		if filepath.Base(dir) == "node_modules" {
			t.Fatalf("watched excluded directory %s", dir)
		}
	}
}
