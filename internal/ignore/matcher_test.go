package ignore

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestGlobalJunkRules(t *testing.T) {
	m := New(Config{})
	cases := []struct {
		path  string
		isDir bool
		skip  bool
	}{
		{"Documents/report.docx", false, false},
		{"Documents/~$report.docx", false, true},
		{"Pictures/.DS_Store", false, true},
		{"Pictures/holiday.jpg", false, false},
		{"Pictures/Thumbs.db", false, true},
		{"Downloads/movie.mkv.part", false, true},
		{"Downloads/ubuntu.iso", false, true},
		{"Downloads/backup.vhdx", false, true},
		{"Videos/wedding.mp4", false, false},
		{"AppData/Local/Temp/x.dat", false, true},
		{"AppData/Local/Google/Chrome/User Data/Default/Cache/f_00001", false, true},
		{"Music/song.mp3", false, false},
		{"Desktop/notes.txt", false, false},
	}
	for _, tc := range cases {
		got := m.Match(tc.path, tc.isDir)
		if got.Skip != tc.skip {
			t.Errorf("Match(%q) skip=%v reason=%q, want skip=%v", tc.path, got.Skip, got.Reason, tc.skip)
		}
		if got.Skip && got.Reason == "" {
			t.Errorf("Match(%q) skipped without a reason", tc.path)
		}
	}
}

// Developer rules must not fire outside projects, otherwise a photographer's
// Pictures/build folder disappears from their backup.
func TestDeveloperRulesAreProjectScoped(t *testing.T) {
	m := New(Config{})
	if d := m.Match("Pictures/build/final.png", false); d.Skip {
		t.Fatalf("Pictures/build should be kept without a project marker, got %q", d.Reason)
	}
	if d := m.Match("Documents/node_modules/readme.md", false); !d.Skip {
		t.Fatal("a literal node_modules directory should still be skipped by the global rule")
	}

	m.RegisterProject(DetectProject("Code/api", []string{"package.json", "src", "node_modules", "dist"}))
	if d := m.Match("Code/api/dist/bundle.js", false); !d.Skip {
		t.Fatal("dist inside a Node project should be skipped")
	}
	if d := m.Match("Code/api/src/index.ts", false); d.Skip {
		t.Fatalf("source files must be backed up, got %q", d.Reason)
	}
	if d := m.Match("Code/api/package.json", false); d.Skip {
		t.Fatalf("the marker file itself must be backed up, got %q", d.Reason)
	}
}

func TestNestedProjectWins(t *testing.T) {
	m := New(Config{})
	m.RegisterProject(DetectProject("Code/mono", []string{"go.mod"}))
	m.RegisterProject(DetectProject("Code/mono/web", []string{"package.json"}))

	if d := m.Match("Code/mono/web/node_modules/react/index.js", false); !d.Skip {
		t.Fatal("node_modules in the nested Node project should be skipped")
	}
	if d := m.Match("Code/mono/vendor/lib.go", false); !d.Skip {
		t.Fatal("vendor in the outer Go module should be skipped")
	}
	if d := m.Match("Code/mono/web/src/App.tsx", false); d.Skip {
		t.Fatalf("nested source must be kept, got %q", d.Reason)
	}
}

func TestProjectLogRuleDoesNotLeak(t *testing.T) {
	m := New(Config{})
	if d := m.Match("Documents/important.log", false); d.Skip {
		t.Fatalf("a user log outside a project must be kept, got %q", d.Reason)
	}
	m.RegisterProject(DetectProject("Code/api", []string{"go.mod"}))
	if d := m.Match("Code/api/logs/server.log", false); !d.Skip {
		t.Fatal("project logs should be skipped")
	}
}

func TestUserIncludeOverridesEverything(t *testing.T) {
	m := New(Config{Include: []string{"Downloads/keep.iso", "!Documents/vendor/"}})
	if d := m.Match("Downloads/keep.iso", false); d.Skip {
		t.Fatalf("explicit include must win, got %q", d.Reason)
	}
	if d := m.Match("Downloads/other.iso", false); !d.Skip {
		t.Fatal("other disk images should still be skipped")
	}
	if d := m.Match("Documents/vendor", true); d.Skip {
		t.Fatalf("include entries may be written with a leading '!', got %q", d.Reason)
	}
}

func TestUserExcludePatterns(t *testing.T) {
	m := New(Config{Exclude: []string{"Documents/Taxes/", "*.bak", "/Desktop/scratch"}})
	for _, p := range []string{"Documents/Taxes/2024.pdf", "Documents/Taxes", "Pictures/old.bak", "Desktop/scratch"} {
		isDir := p == "Documents/Taxes" || p == "Desktop/scratch"
		if d := m.Match(p, isDir); !d.Skip {
			t.Errorf("expected %q to be excluded", p)
		}
	}
	if d := m.Match("Nested/Desktop/scratch", true); d.Skip {
		t.Fatal("a leading slash must anchor the pattern to the backup root")
	}
}

func TestMaxFileSize(t *testing.T) {
	m := New(Config{MaxFileSize: 1 << 20})
	if d := m.MatchFile("Videos/clip.mp4", 2<<20); !d.Skip {
		t.Fatal("expected the oversized file to be skipped")
	}
	if d := m.MatchFile("Videos/clip.mp4", 1<<10); d.Skip {
		t.Fatalf("small files must be kept, got %q", d.Reason)
	}

	unlimited := New(Config{MaxFileSize: -1})
	if d := unlimited.MatchFile("Videos/huge.mp4", 1<<40); d.Skip {
		t.Fatalf("negative MaxFileSize means unlimited, got %q", d.Reason)
	}
}

func TestDisableCategory(t *testing.T) {
	m := New(Config{DisabledCategories: []Category{CategoryVirtualise}})
	if d := m.Match("Downloads/ubuntu.iso", false); d.Skip {
		t.Fatalf("disabling the category should allow disk images, got %q", d.Reason)
	}
}

func TestSystemPathsAreNeverBackedUp(t *testing.T) {
	m := New(Config{})
	var protected, allowed, softDescendant []string
	switch runtime.GOOS {
	case "windows":
		protected = []string{`C:\Windows`, `C:\Windows\System32\ntdll.dll`, `C:\Program Files\App\bin.exe`, `C:\$Recycle.Bin\S-1-5`, `c:\programdata\state.db`}
		allowed = []string{`C:\Users\alice\Documents\a.txt`, `D:\Photos\b.jpg`}
	case "darwin":
		protected = []string{"/System/Library/x", "/usr/bin/ls", "/private/var/db"}
		allowed = []string{"/Users/alice/Documents/a.txt"}
		softDescendant = []string{"/var/folders/xx/T/openbackup-test", "/private/var/folders/xx/T/openbackup-test"}
	default:
		protected = []string{"/proc/1/mem", "/sys/class", "/dev/sda", "/var/lib/docker/overlay2/x", "/usr/bin/ls", "/tmp"}
		allowed = []string{"/home/alice/Documents/a.txt"}
		softDescendant = []string{"/tmp/openbackup-test/home"}
	}
	for _, p := range protected {
		if d := m.IsForbiddenRoot(p); !d.Skip {
			t.Errorf("expected %q to be a forbidden root", p)
		}
	}
	for _, p := range allowed {
		if d := m.IsForbiddenRoot(p); d.Skip {
			t.Errorf("expected %q to be allowed as a root, got %q", p, d.Reason)
		}
	}
	for _, p := range softDescendant {
		if d := m.IsForbiddenRoot(p); d.Skip {
			t.Errorf("expected soft descendant %q to be allowed as a root, got %q", p, d.Reason)
		}
		if d := m.SystemPathOutside(p+"/a.txt", p); d.Skip {
			t.Errorf("expected path under accepted soft root %q to be readable, got %q", p, d.Reason)
		}
	}
}

func TestSystemPathPrefixIsNotSubstring(t *testing.T) {
	m := New(Config{})
	// "/usr" must not match "/usrdata"; prefix checks need a separator.
	var probe string
	if runtime.GOOS == "windows" {
		probe = `C:\Windows Backups\photo.jpg`
	} else {
		probe = "/usrdata/photo.jpg"
	}
	if d := m.IsSystemPath(probe); d.Skip {
		t.Fatalf("expected %q to be allowed, got %q", probe, d.Reason)
	}
}

func TestSkipHidden(t *testing.T) {
	m := New(Config{SkipHidden: true})
	if d := m.Match("Documents/.secret", false); !d.Skip {
		t.Fatal("expected hidden files to be skipped when enabled")
	}
	if d := New(Config{}).Match("Documents/.secret", false); d.Skip {
		t.Fatal("hidden files are kept by default")
	}
}

func TestDetectProject(t *testing.T) {
	cases := []struct {
		names []string
		kind  ProjectKind
	}{
		{[]string{"package.json"}, ProjectNode},
		{[]string{"go.mod", "main.go"}, ProjectGo},
		{[]string{"Cargo.toml"}, ProjectRust},
		{[]string{"pom.xml"}, ProjectJava},
		{[]string{"composer.json"}, ProjectPHP},
		{[]string{"Api.csproj"}, ProjectDotNet},
		{[]string{"Game.uproject"}, ProjectUnreal},
		{[]string{"README.md", "notes.txt"}, ""},
	}
	for _, tc := range cases {
		got := DetectProject("x", tc.names)
		if tc.kind == "" {
			if got != nil {
				t.Errorf("DetectProject(%v) = %v, want nil", tc.names, got.Kind)
			}
			continue
		}
		if got == nil || got.Kind != tc.kind {
			t.Errorf("DetectProject(%v) = %v, want %v", tc.names, got, tc.kind)
		}
	}
}

func TestDetectProjectMergesMarkers(t *testing.T) {
	p := DetectProject("repo", []string{"package.json", "Cargo.toml"})
	if p == nil {
		t.Fatal("expected detection")
	}
	var hasNode, hasRust bool
	for _, d := range p.Excluded {
		switch d {
		case "node_modules":
			hasNode = true
		case "target":
			hasRust = true
		}
	}
	if !hasNode || !hasRust {
		t.Fatalf("polyglot project should exclude both ecosystems, got %v", p.Excluded)
	}
}

func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		isDir   bool
		want    bool
	}{
		{"node_modules/", "a/b/node_modules", true, true},
		{"node_modules/", "a/b/node_modules", false, false},
		{"*.log", "a/b/c.log", false, true},
		{"*.log", "a/b/c.txt", false, false},
		{"/Downloads", "Downloads", true, true},
		{"/Downloads", "a/Downloads", true, false},
		{"**/Cache/", "a/b/c/Cache", true, true},
		{"build/**/*.o", "build/x/y/z.o", false, true},
		{"build/**/*.o", "build/z.o", false, true},
		{"cmake-build-*/", "cmake-build-debug", true, true},
		{"~$*", "~$doc.docx", false, true},
		{"._*", "._resource", false, true},
		{"file[0-9].txt", "file7.txt", false, true},
		{"file[0-9].txt", "filex.txt", false, false},
		{"file[!0-9].txt", "filex.txt", false, true},
		{"a?c", "abc", false, true},
		{"a?c", "abbc", false, false},
	}
	for _, tc := range cases {
		p := ParsePattern(tc.pattern, "test")
		if p == nil {
			t.Fatalf("ParsePattern(%q) returned nil", tc.pattern)
		}
		if got := p.Match(tc.path, tc.isDir, false); got != tc.want {
			t.Errorf("ParsePattern(%q).Match(%q, isDir=%v) = %v, want %v", tc.pattern, tc.path, tc.isDir, got, tc.want)
		}
	}
}

func TestParsePatternSkipsCommentsAndBlanks(t *testing.T) {
	for _, raw := range []string{"", "   ", "# comment", "!", "/"} {
		if p := ParsePattern(raw, ""); p != nil {
			t.Errorf("ParsePattern(%q) should be nil, got %q", raw, p.String())
		}
	}
}

func TestCaseFolding(t *testing.T) {
	p := ParsePattern("*.ISO", "test")
	if !p.Match("Downloads/ubuntu.iso", false, true) {
		t.Fatal("expected case-insensitive match when folding is enabled")
	}
	if p.Match("Downloads/ubuntu.iso", false, false) {
		t.Fatal("expected case-sensitive mismatch when folding is disabled")
	}
}

func TestRelPath(t *testing.T) {
	root := "/home/alice"
	if runtime.GOOS == "windows" {
		root = `C:\Users\alice`
	}
	rel, ok := RelPath(root, filepath.Join(root, "Documents", "a.txt"))
	if !ok || rel != "Documents/a.txt" {
		t.Fatalf("RelPath = %q, %v", rel, ok)
	}
	if _, ok := RelPath(root, "/somewhere/else/a.txt"); ok && runtime.GOOS != "windows" {
		t.Fatal("expected paths outside the root to be rejected")
	}
}
