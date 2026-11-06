package version

import (
	"errors"
	"fmt"
	"strings"
)

// SemVer is a semantic version (https://semver.org/spec/v2.0.0.html).
// Build metadata is stored and ignored when comparing precedence.
type SemVer struct {
	Major int
	Minor int
	Patch int
	Pre   []string
	Build string
}

// Parse parses a semver string. A single leading "v" or "V" is accepted so
// git tags and GitHub Releases compare with the bare version in VERSION.md.
func Parse(raw string) (SemVer, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return SemVer{}, errors.New("empty version")
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	if s == "" {
		return SemVer{}, fmt.Errorf("invalid semver %q", raw)
	}

	build := ""
	if i := strings.IndexByte(s, '+'); i >= 0 {
		build = s[i+1:]
		s = s[:i]
		if err := validateBuild(build); err != nil {
			return SemVer{}, fmt.Errorf("invalid semver %q: %w", raw, err)
		}
	}

	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
		if pre == "" {
			return SemVer{}, fmt.Errorf("invalid semver %q: empty prerelease", raw)
		}
	}

	nums := strings.Split(s, ".")
	if len(nums) != 3 {
		return SemVer{}, fmt.Errorf("invalid semver %q: want MAJOR.MINOR.PATCH", raw)
	}
	major, err := parseNumeric(nums[0])
	if err != nil {
		return SemVer{}, fmt.Errorf("invalid semver %q: major: %w", raw, err)
	}
	minor, err := parseNumeric(nums[1])
	if err != nil {
		return SemVer{}, fmt.Errorf("invalid semver %q: minor: %w", raw, err)
	}
	patch, err := parseNumeric(nums[2])
	if err != nil {
		return SemVer{}, fmt.Errorf("invalid semver %q: patch: %w", raw, err)
	}

	var preParts []string
	if pre != "" {
		preParts = strings.Split(pre, ".")
		for _, part := range preParts {
			if err := validatePreIdent(part); err != nil {
				return SemVer{}, fmt.Errorf("invalid semver %q: %w", raw, err)
			}
		}
	}

	return SemVer{
		Major: major,
		Minor: minor,
		Patch: patch,
		Pre:   preParts,
		Build: build,
	}, nil
}

func parseNumeric(s string) (int, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("invalid numeric identifier %q", s)
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid numeric identifier %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func validatePreIdent(s string) error {
	if s == "" {
		return errors.New("empty prerelease identifier")
	}
	digits := true
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '-':
			digits = false
		default:
			return fmt.Errorf("invalid prerelease identifier %q", s)
		}
	}
	if digits && len(s) > 1 && s[0] == '0' {
		return fmt.Errorf("prerelease numeric identifier %q has a leading zero", s)
	}
	return nil
}

func validateBuild(s string) error {
	if s == "" {
		return errors.New("empty build metadata")
	}
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if part == "" {
			return errors.New("empty build identifier")
		}
		for _, c := range part {
			ok := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-'
			if !ok {
				return fmt.Errorf("invalid build identifier %q", part)
			}
		}
	}
	return nil
}

// String returns the canonical semver without a leading v.
func (v SemVer) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Tag is the git tag and the string stamped into binaries: v plus String.
func (v SemVer) Tag() string {
	return "v" + v.String()
}

// Product is MAJOR.MINOR.PATCH for Windows file versions, which have no
// prerelease field.
func (v SemVer) Product() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare reports precedence. A negative result means v is older than o.
// Build metadata does not affect the result.
func (v SemVer) Compare(o SemVer) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	n := len(v.Pre)
	if len(o.Pre) < n {
		n = len(o.Pre)
	}
	for i := 0; i < n; i++ {
		if c := cmpIdent(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpIdent(a, b string) int {
	an, aErr := parseNumeric(a)
	bn, bErr := parseNumeric(b)
	switch {
	case aErr == nil && bErr == nil:
		return cmpInt(an, bn)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// Highest returns the greatest semver in candidates. Lines that are not
// semver are ignored. ok is false when nothing parsed.
func Highest(candidates []string) (best SemVer, ok bool) {
	for _, c := range candidates {
		v, err := Parse(c)
		if err != nil {
			continue
		}
		if !ok || v.Compare(best) > 0 {
			best = v
			ok = true
		}
	}
	return best, ok
}
