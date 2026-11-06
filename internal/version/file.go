package version

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ReleaseFile is VERSION.md: one canonical semver, then optional notes.
type ReleaseFile struct {
	Version SemVer
	Notes   string
}

// ParseReleaseFile parses VERSION.md contents. The version is the first
// non-empty line that does not start with #. It must be canonical semver
// with no leading v and no build metadata. Everything after that line is notes.
func ParseReleaseFile(content string) (ReleaseFile, error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	var (
		raw   string
		found bool
		notes []string
	)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if !found {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			raw = trim
			found = true
			continue
		}
		notes = append(notes, line)
	}
	if !found {
		return ReleaseFile{}, errors.New("VERSION.md has no version")
	}
	v, err := Parse(raw)
	if err != nil {
		return ReleaseFile{}, err
	}
	if v.Build != "" {
		return ReleaseFile{}, fmt.Errorf("version %q: build metadata is not allowed in VERSION.md", raw)
	}
	if raw != v.String() {
		return ReleaseFile{}, fmt.Errorf("version %q: must be canonical semver without a v prefix (want %s)", raw, v.String())
	}
	return ReleaseFile{
		Version: v,
		Notes:   strings.TrimSpace(strings.Join(notes, "\n")),
	}, nil
}

// ReadReleaseFile reads a VERSION.md file.
func ReadReleaseFile(path string) (ReleaseFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ReleaseFile{}, err
	}
	rel, err := ParseReleaseFile(string(data))
	if err != nil {
		return ReleaseFile{}, fmt.Errorf("%s: %w", path, err)
	}
	return rel, nil
}

// fallbackFromCwd fills an unstamped dev build when the process is started
// inside this repository. Release binaries set Version with -ldflags, and a
// VERSION.md that is not next to the OpenBackup module is ignored.
func fallbackFromCwd() (SemVer, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return SemVer{}, false
	}
	for range 12 {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			mod = bytes.ReplaceAll(mod, []byte("\r\n"), []byte("\n"))
		}
		if err == nil && bytes.Contains(mod, []byte("module github.com/foisalislambd/openbackup\n")) {
			rel, err := ReadReleaseFile(filepath.Join(dir, "VERSION.md"))
			if err != nil {
				return SemVer{}, false
			}
			return rel.Version, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return SemVer{}, false
}

var productVersionRE = regexp.MustCompile(`("productVersion"\s*:\s*")([^"]*)(")`)

// StampWailsProductVersion sets info.productVersion in a wails.json file,
// leaving the rest of the file as it is.
func StampWailsProductVersion(path, product string) error {
	if product == "" || strings.ContainsAny(product, `\"`) {
		return fmt.Errorf("invalid product version %q", product)
	}
	for _, c := range product {
		if (c < '0' || c > '9') && c != '.' {
			return fmt.Errorf("invalid product version %q", product)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !productVersionRE.Match(data) {
		return fmt.Errorf("%s: productVersion field not found", path)
	}
	updated := productVersionRE.ReplaceAll(data, []byte("${1}"+product+"${3}"))
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, info.Mode())
}
