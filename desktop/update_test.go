package main

import "testing"

func TestVersionNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"v0.2.1", "v0.2.0", true},
		{"0.2.1", "v0.2.1", false},
		{"v0.3.0", "v0.2.9", true},
		{"v0.2.0", "v0.2.1", false},
		{"v0.2.1", "dev", true},
		{"", "v0.1.0", false},
		{"v0.2.0", "v0.2.0", false},
	}
	for _, tc := range cases {
		if got := versionNewer(tc.remote, tc.local); got != tc.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", tc.remote, tc.local, got, tc.want)
		}
	}
}

func TestPickDesktopAsset(t *testing.T) {
	t.Parallel()
	rel := githubRelease{
		Assets: []githubAsset{
			{Name: "openbackup-linux-amd64", BrowserDownloadURL: "http://x/cli"},
			{Name: "openbackup-desktop-windows-amd64-setup.exe", BrowserDownloadURL: "http://x/setup"},
			{Name: "openbackup-desktop-windows-amd64.exe", BrowserDownloadURL: "http://x/portable"},
		},
	}
	if got := pickDesktopAsset(rel, "windows", "amd64"); got != "http://x/setup" {
		t.Fatalf("got %q", got)
	}
}
