package ignore

import (
	"strings"
)

// Pattern is a compiled ignore pattern using gitignore-like syntax:
//
//	node_modules/     directory named node_modules at any depth
//	*.iso             any file with the .iso extension
//	/Downloads        Downloads directly under the backup root
//	build/**/*.o      object files under any build directory
//	!keep.log         negation, re-includes a previously excluded path
//
// Matching is case-insensitive on Windows and macOS and case-sensitive on
// Linux, mirroring the behaviour of the host filesystems.
type Pattern struct {
	raw      string
	segments []string
	negate   bool
	dirOnly  bool
	anchored bool
	// reason is a short human readable explanation surfaced in the UI.
	reason string
}

// ParsePattern compiles a single pattern. Blank lines and '#' comments compile
// to a nil pattern so rule files can be read verbatim.
func ParsePattern(raw, reason string) *Pattern {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	p := &Pattern{raw: line, reason: reason}
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}
	line = strings.ReplaceAll(line, "\\", "/")
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if strings.HasPrefix(line, "/") {
		p.anchored = true
		line = strings.TrimPrefix(line, "/")
	} else if strings.Contains(line, "/") {
		// A pattern containing an interior slash is path-relative in gitignore.
		p.anchored = true
	}
	if line == "" {
		return nil
	}
	p.segments = strings.Split(line, "/")
	return p
}

// Reason returns the explanation attached to the pattern.
func (p *Pattern) Reason() string {
	if p.reason != "" {
		return p.reason
	}
	return "matched rule " + p.raw
}

// String returns the original pattern text.
func (p *Pattern) String() string { return p.raw }

// Negated reports whether this pattern re-includes matches.
func (p *Pattern) Negated() bool { return p.negate }

// Match reports whether relPath (slash separated, relative to the backup root)
// matches. isDir must describe the entry itself.
func (p *Pattern) Match(relPath string, isDir bool, fold bool) bool {
	if p == nil || len(p.segments) == 0 {
		return false
	}
	if p.dirOnly && !isDir {
		return false
	}
	pathSegs := strings.Split(strings.Trim(relPath, "/"), "/")
	if p.anchored {
		return matchSegments(p.segments, pathSegs, fold)
	}
	// Unanchored patterns may start at any depth, which is how "node_modules/"
	// catches nested projects.
	for i := range pathSegs {
		if matchSegments(p.segments, pathSegs[i:], fold) {
			return true
		}
	}
	return false
}

// matchSegments matches a pattern segment list against a path segment list,
// where "**" spans zero or more segments. The pattern must consume the whole
// path, except that a directory pattern implicitly covers its contents; that
// containment case is handled by the Matcher, which tests parent directories.
func matchSegments(pat, path []string, fold bool) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive '**'.
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if matchSegments(pat[1:], path[i:], fold) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		if !matchSegment(pat[0], path[0], fold) {
			return false
		}
		pat, path = pat[1:], path[1:]
	}
	return len(path) == 0
}

// matchSegment is a glob matcher for a single path component supporting '*',
// '?' and '[...]' classes. It is written iteratively with backtracking so it
// cannot blow the stack on adversarial names.
func matchSegment(pattern, name string, fold bool) bool {
	if pattern == "*" {
		return true
	}
	if fold {
		pattern = strings.ToLower(pattern)
		name = strings.ToLower(name)
	}
	var (
		p, n           int
		starP, starN   = -1, 0
		hasStarBacktrk bool
	)
	for n < len(name) {
		if p < len(pattern) {
			switch pattern[p] {
			case '*':
				starP, starN, hasStarBacktrk = p, n, true
				p++
				continue
			case '?':
				p++
				n++
				continue
			case '[':
				if end, ok := matchClass(pattern[p:], name[n]); ok {
					p += end
					n++
					continue
				}
			default:
				if pattern[p] == name[n] {
					p++
					n++
					continue
				}
			}
		}
		if hasStarBacktrk {
			starN++
			p, n = starP+1, starN
			continue
		}
		return false
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// matchClass evaluates a '[...]' class at the start of pattern against c and
// returns the class length.
func matchClass(pattern string, c byte) (int, bool) {
	if len(pattern) < 2 {
		return 0, false
	}
	i := 1
	negate := false
	if pattern[i] == '^' || pattern[i] == '!' {
		negate = true
		i++
	}
	matched := false
	first := true
	for i < len(pattern) && (pattern[i] != ']' || first) {
		first = false
		if i+2 < len(pattern) && pattern[i+1] == '-' && pattern[i+2] != ']' {
			if c >= pattern[i] && c <= pattern[i+2] {
				matched = true
			}
			i += 3
			continue
		}
		if pattern[i] == c {
			matched = true
		}
		i++
	}
	if i >= len(pattern) || pattern[i] != ']' {
		return 0, false // unterminated class: treat as a literal '['
	}
	return i + 1, matched != negate
}
