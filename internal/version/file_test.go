package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReleaseFile(t *testing.T) {
	rel, err := ParseReleaseFile("0.3.1\n\nNotes for humans.\n# ignored later? still a note\n")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() != "0.3.1" {
		t.Fatalf("version = %s", rel.Version)
	}
	if !strings.Contains(rel.Notes, "Notes for humans.") {
		t.Fatalf("notes = %q", rel.Notes)
	}

	if _, err := ParseReleaseFile("# comment\n\nv1.2.3\n"); err == nil {
		t.Fatal("leading v should be rejected in VERSION.md")
	}
	if _, err := ParseReleaseFile("1.2.3+sha\n"); err == nil {
		t.Fatal("build metadata should be rejected in VERSION.md")
	}
	if _, err := ParseReleaseFile("\n# only a comment\n"); err == nil {
		t.Fatal("missing version should fail")
	}
}

func TestRepoVersionFile(t *testing.T) {
	root := repoRoot(t)
	rel, err := ReadReleaseFile(filepath.Join(root, "VERSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version.String() == "" {
		t.Fatal("empty version")
	}
	got, ok := fallbackFromCwd()
	if !ok {
		t.Fatal("fallbackFromCwd did not find VERSION.md")
	}
	if got.Compare(rel.Version) != 0 {
		t.Fatalf("fallback %s, file %s", got, rel.Version)
	}
}

func TestStampWailsProductVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wails.json")
	original := "{\n  \"info\": {\n    \"productVersion\": \"0.0.0\"\n  }\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StampWailsProductVersion(path, "0.3.1"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"info\": {\n    \"productVersion\": \"0.3.1\"\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("stamped file:\n%s", got)
	}
	if err := StampWailsProductVersion(path, "0.3.1-rc.1"); err == nil {
		t.Fatal("prerelease product version should be rejected")
	}
}

func TestReadVersionScriptMatchesGo(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "read-version.sh")
	cmd := execCommand(t, "sh", script, filepath.Join(root, "VERSION.md"))
	out := strings.TrimSpace(cmd)
	rel, err := ReadReleaseFile(filepath.Join(root, "VERSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	if out != rel.Version.String() {
		t.Fatalf("script printed %q, Go parsed %q", out, rel.Version)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "VERSION.md")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
