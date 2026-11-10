package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeJoinStaysInsideTheTarget(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"../outside", `..\outside`, "/etc/passwd", "foo/../../outside", "foo/../../../etc/passwd"} {
		got, err := safeJoin(root, name)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, got)
		if err != nil {
			t.Fatal(err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("%q escaped to %s", name, got)
		}
	}
	got, err := safeJoin(root, "Documents/notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "notes.txt" {
		t.Fatalf("path %s", got)
	}
}

func TestRestoreRefusesSymlinkAncestor(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(link, "authorized_keys")
	if err := ensureNoSymlinkAncestors(root, dest); err == nil {
		t.Fatal("restore walked through a symlinked directory")
	}
}
