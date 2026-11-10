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
	"github.com/fsnotify/fsnotify"
)

func TestIgnoreFileUpdateDropsNewlyExcludedWatches(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src", "dist"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.js"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(root, ".openbackupignore"), []byte("secret.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !w.handleEvent(context.Background(), fsnotify.Event{Name: filepath.Join(root, ".openbackupignore"), Op: fsnotify.Write}) {
		t.Fatal("writing the rules file should still be a backup event")
	}
	if w.handleEvent(context.Background(), fsnotify.Event{Name: filepath.Join(root, "secret.txt"), Op: fsnotify.Write}) {
		t.Fatal("a file excluded by the updated rules was queued")
	}
	if !w.handleEvent(context.Background(), fsnotify.Event{Name: filepath.Join(root, "notes.txt"), Op: fsnotify.Write}) {
		t.Fatal("a file the new rules do not mention was dropped")
	}
	for _, dir := range w.watchList() {
		if filepath.Base(dir) == "dist" {
			t.Fatalf("project dist stayed watched: %s", dir)
		}
	}
}
