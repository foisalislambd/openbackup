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
	// fileRules are .openbackupignore patterns keyed by the directory they
	// were found in, relative to the backup root. An empty key is the root.
	fileRules map[string][]*Pattern
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
		cfg:       cfg,
		fold:      caseInsensitiveFS(),
		projects:  make(map[string]*compiledProject),
		fileRules: make(map[string][]*Pattern),
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

// UnregisterProject forgets a project rooted at rel. Used when the marker file
// that created it has been removed.
func (m *Matcher) UnregisterProject(root string) {
	key := m.key(root)
	m.mu.Lock()
	delete(m.projects, key)
	m.mu.Unlock()
}

// IgnoreFileName is the per-directory rules file. It uses the same gitignore
// syntax as the JSON exclude list. It does not read .gitignore: those rules
// are for version control, not for what a person wants restored.
const IgnoreFileName = ".openbackupignore"

// maxIgnoreFile bounds how much of a rules file is parsed. A multi-megabyte
// file in a backup root is not a rules file.
const maxIgnoreFile = 256 << 10

// ObserveDirectory records project markers and a .openbackupignore file found
// among names, which are the immediate child names of rel. readIgnore is
// called only when the rules file is present; a nil return clears any rules
// previously recorded for this directory.
func (m *Matcher) ObserveDirectory(rel string, names []string, readIgnore func() ([]byte, error)) {
	rel = strings.Trim(strings.ReplaceAll(rel, `\`, "/"), "/")
	if rel == "." {
		rel = ""
	}
	if project := DetectProject(rel, names); project != nil {
		m.RegisterProject(project)
	} else {
		m.UnregisterProject(rel)
	}
	var content []byte
	for _, name := range names {
		if name == IgnoreFileName {
			if readIgnore != nil {
				content, _ = readIgnore()
			}
			break
		}
	}
	m.setFileRules(rel, content)
}

func (m *Matcher) setFileRules(dir string, content []byte) {
	key := m.key(dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(content) == 0 {
		delete(m.fileRules, key)
		return
	}
	if len(content) > maxIgnoreFile {
		content = content[:maxIgnoreFile]
	}
	var pats []*Pattern
	for line := range strings.SplitSeq(string(content), "\n") {
		if p := ParsePattern(line, "excluded by .openbackupignore"); p != nil {
			pats = append(pats, p)
		}
	}
	if len(pats) == 0 {
		delete(m.fileRules, key)
		return
	}
	m.fileRules[key] = pats
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
	// Exclude rules follow gitignore semantics: when several rules match, the
	// LAST one wins. First-match-wins would make the common
	//
	//     *.log
	//     !keep.log
	//
	// pair silently drop keep.log forever, which is exactly the kind of quiet
	// data loss a backup tool must not do.
	var excluded *Pattern
	for _, p := range m.exclude {
		for _, c := range cands {
			if p.Match(c.path, c.isDir, m.fold) {
				excluded = p
				break
			}
		}
	}
	if excluded != nil {
		if excluded.Negated() {
			return Decision{}
		}
		return Decision{Skip: true, Reason: excluded.Reason(), Rule: excluded.String()}
	}
	if decided, d := m.matchIgnoreFiles(rel, cands); decided {
		return d
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

// matchIgnoreFiles applies .openbackupignore rules from the backup root down
// to the file. Within that sequence the last matching pattern wins, which is
// how a nested file re-includes something a parent file excluded. A negation
// only cancels an earlier ignore-file pattern; it does not override JSON
// excludes (those are checked first) or the built-in rules.
func (m *Matcher) matchIgnoreFiles(rel string, cands []candidate) (bool, Decision) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.fileRules) == 0 {
		return false, Decision{}
	}
	var last *Pattern
	for _, dir := range ancestorDirs(rel) {
		pats := m.fileRules[m.key(dir)]
		for _, p := range pats {
			for _, c := range cands {
				sub, inside := relativeTo(dir, c.path)
				if !inside || sub == "" {
					continue
				}
				if p.Match(sub, c.isDir, m.fold) {
					last = p
					break
				}
			}
		}
	}
	if last == nil || last.Negated() {
		return false, Decision{}
	}
	return true, Decision{Skip: true, Reason: last.Reason(), Rule: ".openbackupignore: " + last.String()}
}

// ancestorDirs lists the root and each ancestor of rel, root first.
func ancestorDirs(rel string) []string {
	out := []string{""}
	if rel == "" {
		return out
	}
	var b strings.Builder
	for i, part := range strings.Split(rel, "/") {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(part)
		out = append(out, b.String())
		if i == len(strings.Split(rel, "/"))-1 {
			break
		}
	}
	// The file itself is not a directory that holds rules. Drop the last
	// component when it was included by the loop above... the loop includes
	// every prefix including the full path. Rules live in directories, and a
	// directory's rules are stored under that directory. The full path of a
	// file is not a rules key unless a directory has that exact name. Keeping
	// it is harmless (no rules) and needed when rel is the directory.
	return out
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
