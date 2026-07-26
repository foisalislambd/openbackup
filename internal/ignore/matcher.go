package ignore

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Config describes an effective ignore policy. The zero value is the
// recommended zero-configuration policy: every default category enabled.
type Config struct {
	// DisabledCategories turns off a whole group of default rules. Users who
	// really do want their node_modules can disable CategoryDeveloper.
	DisabledCategories []Category
	// Exclude holds extra user patterns in gitignore syntax.
	Exclude []string
	// Include holds user patterns that win over every exclusion, expressed
	// without the leading '!'.
	Include []string
	// MaxFileSize skips files larger than this. Zero uses DefaultMaxFileSize,
	// negative means unlimited.
	MaxFileSize int64
	// SkipHidden also skips dotfiles and Windows hidden entries.
	SkipHidden bool
}

// Decision is the result of evaluating a path.
type Decision struct {
	// Skip reports whether the entry must not be backed up.
	Skip bool
	// Reason is a user-facing explanation, empty when Skip is false.
	Reason string
	// Rule is the pattern or path prefix responsible.
	Rule string
	// Category is the rule group responsible.
	Category Category
}

// Matcher evaluates ignore rules. It is safe for concurrent use: the scanner
// walks directories in parallel and registers detected projects as it goes.
type Matcher struct {
	cfg Config
	// systemRoots are absolute prefixes that are never traversed.
	systemRoots []Rule
	// softRoots are systemRoots that may still be chosen as an explicit backup
	// root when the path is a descendant (see IsForbiddenRoot).
	softRoots map[string]struct{}
	// global holds patterns evaluated against every relative path.
	global []categorised
	// devPatterns are evaluated only inside detected project roots.
	devPatterns []categorised
	// include patterns override all exclusions.
	include []*Pattern
	// exclude holds the user's own patterns.
	exclude []*Pattern

	maxFileSize int64
	fold        bool

	mu       sync.RWMutex
	projects map[string]*compiledProject
}

type categorised struct {
	pattern  *Pattern
	category Category
}

type compiledProject struct {
	project  *Project
	patterns []*Pattern
}

// New compiles a Matcher from cfg.
func New(cfg Config) *Matcher {
	disabled := make(map[Category]bool, len(cfg.DisabledCategories))
	for _, c := range cfg.DisabledCategories {
		disabled[c] = true
	}

	m := &Matcher{
		cfg:      cfg,
		fold:     caseInsensitiveFS(),
		projects: make(map[string]*compiledProject),
	}

	switch {
	case cfg.MaxFileSize == 0:
		m.maxFileSize = DefaultMaxFileSize
	case cfg.MaxFileSize < 0:
		m.maxFileSize = 0 // unlimited
	default:
		m.maxFileSize = cfg.MaxFileSize
	}

	if !disabled[CategorySystem] {
		m.systemRoots = SystemRootPaths()
		m.softRoots = softSystemPrefixes(runtime.GOOS)
	}
	addAll := func(cat Category, rules []Rule) {
		if disabled[cat] {
			return
		}
		for _, r := range rules {
			if p := ParsePattern(r.Pattern, r.Reason); p != nil {
				m.global = append(m.global, categorised{pattern: p, category: cat})
			}
		}
	}
	addAll(CategoryJunk, junkRules())
	addAll(CategoryCache, cacheRules())
	addAll(CategoryVirtualise, virtualisationRules())
	addAll(CategoryEphemeral, ephemeralRules())
	addAll(CategoryDeveloper, developerGlobalRules())

	if !disabled[CategoryDeveloper] {
		for _, r := range developerScopedRules() {
			if p := ParsePattern(r.Pattern, r.Reason); p != nil {
				m.devPatterns = append(m.devPatterns, categorised{pattern: p, category: CategoryDeveloper})
			}
		}
	}
	for _, raw := range cfg.Exclude {
		if p := ParsePattern(raw, "excluded by your rules"); p != nil {
			m.exclude = append(m.exclude, p)
		}
	}
	for _, raw := range cfg.Include {
		raw = strings.TrimPrefix(strings.TrimSpace(raw), "!")
		if p := ParsePattern(raw, "always included by your rules"); p != nil {
			m.include = append(m.include, p)
		}
	}
	return m
}

// MaxFileSize reports the effective size cut-off; zero means unlimited.
func (m *Matcher) MaxFileSize() int64 { return m.maxFileSize }

// IsSystemPath reports whether abs falls inside a protected OS location. Soft
// prefixes (temp dirs, mount points) still match so a walk that escapes into
// them is stopped; use IsForbiddenRoot to decide whether a path may be a backup
// root, and SystemPathOutside when walking inside an already-accepted root.
func (m *Matcher) IsSystemPath(abs string) Decision {
	norm := normalizeAbs(abs)
	if norm == "" {
		return Decision{}
	}
	for _, r := range m.systemRoots {
		if norm == r.Pattern || strings.HasPrefix(norm, r.Pattern+"/") {
			return Decision{Skip: true, Reason: r.Reason, Rule: r.Pattern, Category: CategorySystem}
		}
	}
	return Decision{}
}

// IsForbiddenRoot reports whether abs must not be configured as a backup root.
// Hard system prefixes are always refused. Soft prefixes are refused only when
// the path is exactly the prefix (backing up all of /tmp), so a folder inside
// the OS temp directory — including Go's testing.TempDir — can still be added.
func (m *Matcher) IsForbiddenRoot(abs string) Decision {
	norm := normalizeAbs(abs)
	if norm == "" {
		return Decision{}
	}
	for _, r := range m.systemRoots {
		if norm == r.Pattern {
			return Decision{Skip: true, Reason: r.Reason, Rule: r.Pattern, Category: CategorySystem}
		}
		if strings.HasPrefix(norm, r.Pattern+"/") {
			if _, soft := m.softRoots[r.Pattern]; soft {
				continue
			}
			return Decision{Skip: true, Reason: r.Reason, Rule: r.Pattern, Category: CategorySystem}
		}
	}
	return Decision{}
}

// SystemPathOutside reports a protected location reached from root that is not
// part of the tree the user asked to back up — typically a Windows junction
// into C:\Windows. Paths that stay under root are allowed even when root itself
// sits under a soft system prefix such as /tmp.
func (m *Matcher) SystemPathOutside(abs, root string) Decision {
	d := m.IsSystemPath(abs)
	if !d.Skip {
		return Decision{}
	}
	rootNorm := normalizeAbs(root)
	absNorm := normalizeAbs(abs)
	if rootNorm == "" || absNorm == "" {
		return d
	}
	if absNorm == rootNorm || strings.HasPrefix(absNorm, rootNorm+"/") {
		if rootDec := m.IsSystemPath(root); rootDec.Skip && rootDec.Rule == d.Rule {
			return Decision{}
		}
	}
	return d
}

// RegisterProject records a detected project so that developer rules apply
// beneath it. root must be the project directory relative to the backup root.
func (m *Matcher) RegisterProject(p *Project) {
	if p == nil {
		return
	}
	cp := &compiledProject{project: p}
	for _, dir := range p.Excluded {
		if dir == "" {
			continue
		}
		// Anchor to the project root and mark as directory-only unless the rule
		// is a glob such as *.egg-info, which may match either.
		pattern := "/" + dir
		if !strings.ContainsAny(dir, "*?[") {
			pattern += "/"
		}
		if cpat := ParsePattern(pattern, p.Reason); cpat != nil {
			cp.patterns = append(cp.patterns, cpat)
		}
	}
	key := m.key(p.Root)
	m.mu.Lock()
	m.projects[key] = cp
	m.mu.Unlock()
}

// Projects returns the projects detected so far.
func (m *Matcher) Projects() []*Project {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Project, 0, len(m.projects))
	for _, cp := range m.projects {
		out = append(out, cp.project)
	}
	return out
}

// candidate is a path that must be tested against the rules: either the entry
// itself or one of its ancestor directories.
type candidate struct {
	path  string
	isDir bool
}

// candidates returns the entry followed by each of its ancestor directories.
//
// Testing ancestors matters because excluding a directory must exclude
// everything inside it. The scanner normally prunes an excluded directory
// before descending, but Match is also called directly by the file watcher,
// which sees a deep path like ".../node_modules/react/index.js" with no
// knowledge of the walk that would have pruned it.
func candidates(rel string, isDir bool) []candidate {
	out := make([]candidate, 0, 8)
	out = append(out, candidate{path: rel, isDir: isDir})
	for {
		idx := strings.LastIndex(rel, "/")
		if idx <= 0 {
			return out
		}
		rel = rel[:idx]
		out = append(out, candidate{path: rel, isDir: true})
	}
}

// Match evaluates a path relative to the backup root. relPath must use forward
// slashes; isDir describes the entry.
func (m *Matcher) Match(relPath string, isDir bool) Decision {
	rel := strings.Trim(strings.ReplaceAll(relPath, `\`, "/"), "/")
	if rel == "" || rel == "." {
		return Decision{}
	}
	cands := candidates(rel, isDir)

	// User includes are absolute overrides so nobody can lose a folder they
	// explicitly asked for.
	for _, p := range m.include {
		for _, c := range cands {
			if p.Match(c.path, c.isDir, m.fold) {
				return Decision{}
			}
		}
	}
	for _, p := range m.exclude {
		for _, c := range cands {
			if p.Match(c.path, c.isDir, m.fold) {
				if p.Negated() {
					return Decision{}
				}
				return Decision{Skip: true, Reason: p.Reason(), Rule: p.String()}
			}
		}
	}
	if m.cfg.SkipHidden {
		for _, c := range cands {
			if isHiddenName(c.path) {
				return Decision{Skip: true, Reason: "hidden entry", Rule: "hidden", Category: CategoryJunk}
			}
		}
	}
	for _, g := range m.global {
		for _, c := range cands {
			if g.pattern.Match(c.path, c.isDir, m.fold) {
				return Decision{Skip: true, Reason: g.pattern.Reason(), Rule: g.pattern.String(), Category: g.category}
			}
		}
	}
	return m.matchProject(rel, cands)
}

// matchProject applies developer rules within the innermost detected project
// containing rel.
func (m *Matcher) matchProject(rel string, cands []candidate) Decision {
	if len(m.devPatterns) == 0 {
		return Decision{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.projects) == 0 {
		return Decision{}
	}

	// Walk from the entry upwards so the innermost project wins; a monorepo's
	// nested package.json governs its own subtree.
	dir := rel
	for {
		idx := strings.LastIndex(dir, "/")
		if idx < 0 {
			dir = ""
		} else {
			dir = dir[:idx]
		}
		cp, ok := m.projects[m.key(dir)]
		if ok {
			for _, c := range cands {
				sub, inside := relativeTo(dir, c.path)
				if !inside || sub == "" {
					continue
				}
				for _, p := range cp.patterns {
					if p.Match(sub, c.isDir, m.fold) {
						return Decision{Skip: true, Reason: p.Reason(), Rule: cp.project.Marker + " -> " + p.String(), Category: CategoryDeveloper}
					}
				}
				for _, d := range m.devPatterns {
					if d.pattern.Match(sub, c.isDir, m.fold) {
						return Decision{Skip: true, Reason: d.pattern.Reason(), Rule: d.pattern.String(), Category: CategoryDeveloper}
					}
				}
			}
			return Decision{}
		}
		if dir == "" {
			return Decision{}
		}
	}
}

// relativeTo strips the base directory prefix from p.
func relativeTo(base, p string) (string, bool) {
	if base == "" {
		return p, true
	}
	if !strings.HasPrefix(p, base) {
		return "", false
	}
	rest := p[len(base):]
	if rest == "" {
		return "", true
	}
	if rest[0] != '/' {
		return "", false
	}
	return rest[1:], true
}

// MatchFile evaluates a file including its size.
func (m *Matcher) MatchFile(relPath string, size int64) Decision {
	if d := m.Match(relPath, false); d.Skip {
		return d
	}
	if m.maxFileSize > 0 && size > m.maxFileSize {
		return Decision{
			Skip:     true,
			Reason:   "larger than the configured maximum file size",
			Rule:     "max-file-size",
			Category: CategoryEphemeral,
		}
	}
	return Decision{}
}

func (m *Matcher) key(p string) string {
	p = strings.Trim(strings.ReplaceAll(p, `\`, "/"), "/")
	if m.fold {
		return strings.ToLower(p)
	}
	return p
}

// isHiddenName reports whether the final component starts with a dot.
func isHiddenName(rel string) bool {
	base := rel
	if idx := strings.LastIndex(rel, "/"); idx >= 0 {
		base = rel[idx+1:]
	}
	return strings.HasPrefix(base, ".") && base != "." && base != ".."
}

// RelPath converts an absolute path into a slash-separated path relative to
// root, or returns false when abs is outside root.
func RelPath(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = strings.ReplaceAll(rel, `\`, "/")
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
