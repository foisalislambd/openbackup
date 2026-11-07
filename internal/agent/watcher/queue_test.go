package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/ignore"
)

func TestProjectBuildDirectoryIsNotWatched(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src", "dist"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.js"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "app.js"), []byte("built"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(ignore.New(ignore.Config{}), time.Second, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.AddRoots(context.Background(), []config.Root{{Name: "proj", Path: root, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range w.watchList() {
		if filepath.Base(dir) == "dist" {
			t.Fatalf("watched build output: %s", dir)
		}
	}
}
