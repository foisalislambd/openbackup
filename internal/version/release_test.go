package version

import "testing"

func mustParse(t *testing.T, s string) SemVer {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDecideRelease(t *testing.T) {
	current := mustParse(t, "0.3.1")
	released := []string{"v0.2.9", "v0.3.0", "nightly"}

	d := DecideRelease(current, released)
	if d.Skip {
		t.Fatalf("expected release, message %s", d.Message)
	}
	if d.Tag != "v0.3.1" || d.Version != "0.3.1" || d.Product != "0.3.1" {
		t.Fatalf("decision = %+v", d)
	}

	same := DecideRelease(mustParse(t, "0.3.0"), released)
	if !same.Skip {
		t.Fatal("equal version should skip")
	}
	older := DecideRelease(mustParse(t, "0.2.9"), released)
	if !older.Skip {
		t.Fatal("older version should skip")
	}
	first := DecideRelease(current, []string{"nope"})
	if first.Skip {
		t.Fatal("no prior release should publish")
	}

	// 0.3.0 is not greater than 0.10.0 even though the text sorts later.
	if !DecideRelease(mustParse(t, "0.3.1"), []string{"v0.10.0"}).Skip {
		t.Fatal("numeric compare should treat 0.10.0 as newer than 0.3.1")
	}
	pre := DecideRelease(mustParse(t, "1.0.0-rc.1"), []string{"v1.0.0-rc.1"})
	if !pre.Skip {
		t.Fatal("same prerelease should skip")
	}
	if DecideRelease(mustParse(t, "1.0.0"), []string{"v1.0.0-rc.1"}).Skip {
		t.Fatal("release should be newer than its prerelease")
	}
}

func TestCheckPullRequest(t *testing.T) {
	current := mustParse(t, "0.3.1")
	released := []string{"v0.3.0"}

	status, msg := CheckPullRequest(current, released, true)
	if status != "ok" || msg == "" {
		t.Fatalf("newer version: %s %s", status, msg)
	}

	status, msg = CheckPullRequest(mustParse(t, "0.3.0"), released, false)
	if status != "warn" {
		t.Fatalf("unchanged released version: %s %s", status, msg)
	}
	status, msg = CheckPullRequest(mustParse(t, "0.3.0"), released, true)
	if status != "fail" {
		t.Fatalf("changed but not newer: %s %s", status, msg)
	}
	status, msg = CheckPullRequest(mustParse(t, "0.2.0"), released, true)
	if status != "fail" {
		t.Fatalf("lowered version: %s %s", status, msg)
	}
}
