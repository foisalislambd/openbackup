package index

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestUnchangedUsesInodeWhenBothSidesHaveOne(t *testing.T) {
	ctx := context.Background()
	idx, err := Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	when := time.Now().UTC().Truncate(time.Second)
	state := FileState{Path: "/data/a", Size: 8, ModTime: when, Digest: "abc", Chunks: []string{"abc"}, Inode: 7}
	if err := idx.Put(ctx, state, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Unchanged(ctx, state.Path, 8, when, 7); !ok {
		t.Fatal("same inode should be unchanged")
	}
	if _, ok := idx.Unchanged(ctx, state.Path, 8, when, 0); !ok {
		t.Fatal("a missing inode must not force a re-read")
	}
	if _, ok := idx.Unchanged(ctx, state.Path, 8, when, 9); ok {
		t.Fatal("a different inode is a different file")
	}
	if err := idx.Apply(ctx, []Change{{Path: state.Path, Generation: 2, Inode: 9}}); err != nil {
		t.Fatal(err)
	}
	got, ok := idx.Lookup(ctx, state.Path)
	if !ok || got.Inode != 9 {
		t.Fatalf("touch should record the inode, got %+v", got)
	}
}
