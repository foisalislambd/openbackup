package ignore

import "testing"

func TestOpenBackupIgnoreLastMatchAndPrecedence(t *testing.T) {
	m := New(Config{
		Include: []string{"keep/**"},
		Exclude: []string{"secret/**"},
	})
	m.ObserveDirectory("", []string{".openbackupignore", "notes.skip", "notes.tmp", "keep", "secret"}, func() ([]byte, error) {
		return []byte("*.skip\n!notes.skip\n!notes.tmp\n"), nil
	})
	if d := m.Match("notes.skip", false); d.Skip {
		t.Fatalf("negation should keep notes.skip, got %s", d.Reason)
	}
	if d := m.Match("other.skip", false); !d.Skip {
		t.Fatal("*.skip should skip other.skip")
	}
	if d := m.Match("notes.tmp", false); !d.Skip {
		t.Fatal("a .openbackupignore negation must not undo the built-in temporary-file rule")
	}
	if d := m.Match("keep/other.skip", false); d.Skip {
		t.Fatal("an include pattern outranks .openbackupignore")
	}
	if d := m.Match("secret/notes.skip", false); !d.Skip {
		t.Fatal("a configured exclude outranks .openbackupignore negation")
	}
	m.ObserveDirectory("proj", []string{"package.json", "dist", "src"}, nil)
	if d := m.Match("proj/dist", true); !d.Skip {
		t.Fatal("dist next to package.json should be skipped")
	}
	if d := m.Match("album/dist", true); d.Skip {
		t.Fatal("dist outside a project should be kept")
	}
	if d := m.Match("album/node_modules", true); !d.Skip {
		t.Fatal("node_modules is excluded wherever it appears")
	}
}
