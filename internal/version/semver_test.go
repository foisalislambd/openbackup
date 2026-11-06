package version

import "testing"

func TestParseAndCompare(t *testing.T) {
	// Precedence examples from the SemVer spec.
	order := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	parsed := make([]SemVer, len(order))
	for i, s := range order {
		v, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		parsed[i] = v
		if v.String() != s {
			t.Fatalf("String(%q) = %q", s, v.String())
		}
	}
	for i := 0; i < len(parsed); i++ {
		for j := 0; j < len(parsed); j++ {
			got := parsed[i].Compare(parsed[j])
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestParseAcceptsTagPrefixAndIgnoresBuild(t *testing.T) {
	a, err := Parse("v1.2.3+build.7")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("V1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if a.Compare(b) != 0 {
		t.Fatalf("build metadata changed precedence: %s vs %s", a, b)
	}
	if a.Tag() != "v1.2.3+build.7" {
		t.Fatalf("tag = %s", a.Tag())
	}
	if a.Product() != "1.2.3" {
		t.Fatalf("product = %s", a.Product())
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"v",
		"1",
		"1.2",
		"1.2.3.4",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.3-",
		"1.2.3+",
		"1.2.3-01",
		"1.2.3-rc.01",
		"1.2.3-alpha..1",
		"1.2.3-α",
		"latest",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", s)
		}
	}
}

func TestHighest(t *testing.T) {
	best, ok := Highest([]string{"v0.2.9", "not-a-version", "v0.3.0", "v0.2.10", "v0.10.0"})
	if !ok {
		t.Fatal("expected a version")
	}
	if best.String() != "0.10.0" {
		t.Fatalf("highest = %s, want 0.10.0", best)
	}
	if _, ok := Highest([]string{"", "nope"}); ok {
		t.Fatal("expected no version")
	}
}
