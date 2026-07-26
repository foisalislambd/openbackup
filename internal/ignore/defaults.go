package ignore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Rule is a default ignore pattern with the explanation shown to the user.
// Every default rule carries a reason so the dashboard can answer "why was
// this folder skipped?" instead of silently omitting data.
type Rule struct {
	Pattern string
	Reason  string
}

// Category groups the default rules so the UI can offer per-category toggles.
type Category string

// Rule categories.
const (
	CategorySystem     Category = "system"
	CategoryJunk       Category = "junk"
	CategoryCache      Category = "cache"
	CategoryDeveloper  Category = "developer"
	CategoryVirtualise Category = "virtualisation"
	CategoryEphemeral  Category = "ephemeral"
)

// DefaultMaxFileSize is the per-file cut-off above which files are skipped
// unless the user opts in. It protects users from silently uploading a 200 GiB
// VM disk over a home connection.
const DefaultMaxFileSize int64 = 8 << 30 // 8 GiB

// systemRoots returns absolute directory prefixes that must never be read for
// backup, regardless of what the user selects. These are OS and application
// installations: restoring them onto a different machine is useless at best and
// harmful at worst, and reading them wastes hours of IO.
func systemRoots(goos string) []Rule {
	switch goos {
	case "windows":
		roots := []Rule{
			{`C:\Windows`, "Windows operating system files"},
			{`C:\Program Files`, "installed software, not user data"},
			{`C:\Program Files (x86)`, "installed software, not user data"},
			{`C:\ProgramData`, "machine-wide application state"},
			{`C:\Recovery`, "Windows recovery partition data"},
			{`C:\$Recycle.Bin`, "recycle bin"},
			{`C:\System Volume Information`, "Windows restore points and indexes"},
			{`C:\PerfLogs`, "performance logs"},
			{`C:\hiberfil.sys`, "hibernation image"},
			{`C:\pagefile.sys`, "virtual memory page file"},
			{`C:\swapfile.sys`, "virtual memory swap file"},
			{`C:\DumpStack.log.tmp`, "crash dump scratch file"},
			{`C:\Config.Msi`, "installer scratch directory"},
		}
		// Honour non-default Windows installs (a D: drive system volume, for
		// example) instead of hard-coding C:.
		for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if v := os.Getenv(env); v != "" {
				roots = append(roots, Rule{v, "system location from %" + env + "%"})
			}
		}
		if sd := os.Getenv("SystemDrive"); sd != "" {
			for _, name := range []string{`$Recycle.Bin`, `System Volume Information`, `Recovery`, `$WinREAgent`, `Windows.old`} {
				roots = append(roots, Rule{sd + `\` + name, "Windows system location"})
			}
		}
		return roots
	case "darwin":
		return []Rule{
			{"/System", "macOS system volume"},
			{"/Library", "machine-wide application support"},
			{"/Applications", "installed applications"},
			{"/usr", "operating system binaries and libraries"},
			{"/bin", "operating system binaries"},
			{"/sbin", "operating system binaries"},
			{"/etc", "system configuration"},
			{"/var/db", "system database"},
			{"/var/root", "root home directory"},
			{"/var/log", "system logs"},
			{"/var/vm", "virtual memory files"},
			{"/var/tmp", "temporary files"},
			{"/var/folders", "per-user temporary files"},
			{"/private/etc", "system configuration"},
			{"/private/var/db", "system database"},
			{"/private/var/root", "root home directory"},
			{"/private/var/log", "system logs"},
			{"/private/var/vm", "virtual memory files"},
			{"/private/var/folders", "per-user temporary files"},
			{"/tmp", "temporary files"},
			{"/private/tmp", "temporary files"},
			{"/cores", "kernel core dumps"},
			{"/dev", "device nodes"},
			{"/Volumes", "mounted volumes; add specific drives explicitly"},
			{"/.Spotlight-V100", "Spotlight index"},
			{"/.fseventsd", "filesystem event log"},
			{"/.DocumentRevisions-V100", "macOS document revisions store"},
			{"/.Trashes", "trash"},
			{"/.vol", "volume metadata"},
		}
	default: // linux and other unix
		return []Rule{
			{"/proc", "kernel process interface"},
			{"/sys", "kernel object interface"},
			{"/dev", "device nodes"},
			{"/run", "runtime state"},
			{"/tmp", "temporary files"},
			{"/var/tmp", "temporary files"},
			{"/var/run", "runtime state"},
			{"/var/cache", "package and application caches"},
			{"/var/lib/docker", "container layers, rebuildable"},
			{"/var/lib/containers", "container layers, rebuildable"},
			{"/usr", "operating system files"},
			{"/bin", "operating system binaries"},
			{"/sbin", "operating system binaries"},
			{"/lib", "operating system libraries"},
			{"/lib64", "operating system libraries"},
			{"/boot", "bootloader and kernel images"},
			{"/etc", "system configuration"},
			{"/opt", "installed software"},
			{"/srv", "service data; add explicitly if wanted"},
			{"/snap", "snap package mounts"},
			{"/lost+found", "filesystem recovery area"},
			{"/swapfile", "virtual memory swap file"},
			{"/swap.img", "virtual memory swap file"},
			{"/mnt", "mount points; add specific drives explicitly"},
			{"/media", "removable media; add specific drives explicitly"},
		}
	}
}

// softSystemPrefixes are system roots a user may still choose explicitly as a
// backup folder. Walks that escape into them from elsewhere still stop, but
// Go's testing.TempDir (under /tmp or /var/folders) must be backable or every
// agent test is empty. Exact prefixes such as /tmp itself remain forbidden.
func softSystemPrefixes(goos string) map[string]struct{} {
	var list []string
	switch goos {
	case "windows":
		// No soft prefixes: Windows temp is under the user profile, not a
		// protected system root.
	case "darwin":
		list = []string{
			"/tmp", "/private/tmp",
			"/var/folders", "/private/var/folders",
			"/cores", "/Volumes",
		}
	default:
		list = []string{"/tmp", "/var/tmp", "/run", "/mnt", "/media"}
	}
	out := make(map[string]struct{}, len(list))
	for _, p := range list {
		if clean := normalizeAbs(p); clean != "" {
			out[clean] = struct{}{}
		}
	}
	return out
}

// junkRules cover files that exist only to serve a local UI or filesystem and
// are regenerated automatically.
func junkRules() []Rule {
	return []Rule{
		{"desktop.ini", "Windows folder view settings"},
		{"Thumbs.db", "Windows thumbnail cache"},
		{"ehthumbs.db", "Windows thumbnail cache"},
		{".DS_Store", "macOS folder view settings"},
		{"._*", "macOS resource fork sidecar"},
		{".Spotlight-V100/", "Spotlight index"},
		{".fseventsd/", "filesystem event log"},
		{".Trash/", "trash"},
		{".Trash-*/", "trash"},
		{"$RECYCLE.BIN/", "recycle bin"},
		{"*.lnk", "Windows shortcut, target is backed up instead"},
		{"*.tmp", "temporary file"},
		{"*.temp", "temporary file"},
		{"~$*", "Office lock file"},
		{".~lock.*#", "LibreOffice lock file"},
		{"*.crdownload", "incomplete browser download"},
		{"*.part", "incomplete download"},
		{"*.partial", "incomplete download"},
		{"*.swp", "editor swap file"},
		{"*.swo", "editor swap file"},
		{"*~", "editor backup file"},
		{"nohup.out", "stray process output"},
	}
}

// anyDepth rewrites multi-segment patterns so they match at any depth.
//
// In gitignore syntax a pattern containing an interior slash is anchored to the
// root, but a cache path such as "AppData/Local/Temp" must be caught whether
// the backup root is the user's home directory or a whole drive, so these rules
// opt out of anchoring.
func anyDepth(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	for i, r := range rules {
		p := r.Pattern
		if strings.Contains(p, "/") && !strings.HasPrefix(p, "**/") && !strings.HasPrefix(p, "/") &&
			strings.Trim(p, "/") != "" && strings.Contains(strings.Trim(p, "/"), "/") {
			p = "**/" + p
		}
		out[i] = Rule{Pattern: p, Reason: r.Reason}
	}
	return out
}

// cacheRules cover regenerable caches. Browser profiles are the single largest
// source of useless backup churn on a typical desktop: a Chrome cache can
// rewrite hundreds of megabytes per hour.
func cacheRules() []Rule {
	return anyDepth([]Rule{
		{"**/Cache/", "regenerable cache"},
		{"**/Cache_Data/", "regenerable cache"},
		{"**/CacheStorage/", "regenerable cache"},
		{"**/Code Cache/", "browser code cache"},
		{"**/GPUCache/", "GPU shader cache"},
		{"**/ShaderCache/", "GPU shader cache"},
		{"**/GrShaderCache/", "GPU shader cache"},
		{"**/Service Worker/CacheStorage/", "service worker cache"},
		{"**/Crashpad/", "crash reporter state"},
		{"**/CrashReports/", "crash reports"},
		{"**/component_crx_cache/", "browser component cache"},
		{".cache/", "generic cache directory"},
		{".npm/_cacache/", "npm package cache"},
		{".yarn/cache/", "Yarn package cache"},
		{".pnpm-store/", "pnpm content store"},
		{".bun/install/cache/", "Bun package cache"},
		{".gradle/caches/", "Gradle cache"},
		{".m2/repository/", "Maven repository cache"},
		{".ivy2/cache/", "Ivy cache"},
		{".sbt/boot/", "sbt boot cache"},
		{".cargo/registry/", "Cargo registry cache"},
		{".cargo/git/", "Cargo git checkout cache"},
		{".rustup/toolchains/", "Rust toolchains, reinstallable"},
		{".go/pkg/mod/", "Go module cache"},
		{"go/pkg/mod/", "Go module cache"},
		{"go/pkg/sumdb/", "Go checksum database cache"},
		{".nuget/packages/", "NuGet package cache"},
		{".composer/cache/", "Composer cache"},
		{".gem/", "RubyGems cache"},
		{".pub-cache/", "Dart/Flutter package cache"},
		{".cocoapods/", "CocoaPods cache"},
		{".conda/pkgs/", "Conda package cache"},
		{".docker/", "Docker client state and caches"},
		{".vscode/extensions/", "editor extensions, reinstallable"},
		{".android/avd/", "Android emulator images, rebuildable"},
		{"Library/Caches/", "macOS user caches"},
		{"AppData/Local/Temp/", "Windows user temp"},
		{"AppData/Local/Microsoft/Windows/INetCache/", "Internet Explorer cache"},
		{"AppData/Local/Microsoft/Windows/Explorer/", "Explorer thumbnail cache"},
		{"AppData/Local/Packages/", "Windows Store app state"},
		{"AppData/Local/CrashDumps/", "crash dumps"},
		{"AppData/LocalLow/", "low-integrity app scratch data"},
		{"OneDriveTemp/", "OneDrive scratch directory"},
	})
}

// developerGlobalRules exclude dependency trees and build caches whose names
// are unambiguous. A directory called "node_modules" or "__pycache__" is never
// user data, wherever it appears, so these apply everywhere.
func developerGlobalRules() []Rule {
	return []Rule{
		{"node_modules/", "reinstallable with npm install"},
		{"bower_components/", "reinstallable dependency directory"},
		{"jspm_packages/", "reinstallable dependency directory"},
		{".next/", "Next.js build cache"},
		{".nuxt/", "Nuxt build cache"},
		{".svelte-kit/", "SvelteKit build cache"},
		{".astro/", "Astro build cache"},
		{".angular/", "Angular build cache"},
		{".parcel-cache/", "Parcel cache"},
		{".turbo/", "Turborepo cache"},
		{".nx/cache/", "Nx cache"},
		{".vite/", "Vite cache"},
		{".webpack/", "webpack cache"},
		{".nyc_output/", "coverage scratch data"},
		{".pytest_cache/", "pytest cache"},
		{".mypy_cache/", "mypy cache"},
		{".ruff_cache/", "ruff cache"},
		{".tox/", "tox environments"},
		{"__pycache__/", "Python bytecode cache"},
		{"*.pyc", "Python bytecode"},
		{"*.pyo", "Python bytecode"},
		{".venv/", "Python virtual environment, recreatable"},
		{".terraform/", "Terraform provider plugins"},
		{".serverless/", "Serverless framework build output"},
		{".dart_tool/", "Dart build state"},
		{".gradle/", "Gradle project state"},
		{".idea/", "IDE local workspace state"},
		{".vs/", "Visual Studio local state"},
		{"CMakeFiles/", "CMake scratch directory"},
		{"cmake-build-*/", "CMake build directory"},
		{"DerivedData/", "Xcode build output"},
		{"*.xcworkspace/xcuserdata/", "Xcode per-user state"},
		{"*.class", "compiled Java class"},
		{"*.egg-info/", "Python package metadata, regenerated on build"},
	}
}

// developerScopedRules exclude directory names that are only build output *in
// the context of a project*. They are deliberately scoped to directories where
// a project marker was detected (see project.go): applying "build/" or "bin/"
// globally would silently drop a photographer's ~/Pictures/build folder, and
// quietly losing user data is a far worse failure than storing a few megabytes
// of build output.
func developerScopedRules() []Rule {
	return []Rule{
		{"vendor/", "vendored dependencies, reinstallable"},
		{"target/", "build output"},
		{"build/", "build output"},
		{"dist/", "build output"},
		{"out/", "build output"},
		{"bin/", "build output"},
		{"obj/", "build output"},
		{".build/", "build output"},
		{"coverage/", "test coverage report"},
		{"venv/", "Python virtual environment, recreatable"},
		{"env/", "Python virtual environment, recreatable"},
		{"Pods/", "CocoaPods checkout, reinstallable"},
		{"logs/", "project log directory"},
		{"*.log", "log file"},
		{"tmp/", "project scratch directory"},
		{"*.o", "object file"},
		{"*.a", "static library build output"},
	}
}

// virtualisationRules exclude very large, constantly-rewritten disk images.
// Backing up a running VM image is both enormous and usually inconsistent, so
// the default is to skip and let the user opt in per folder.
func virtualisationRules() []Rule {
	return []Rule{
		{"*.vdi", "VirtualBox disk image"},
		{"*.vmdk", "VMware disk image"},
		{"*.vhd", "Hyper-V disk image"},
		{"*.vhdx", "Hyper-V disk image"},
		{"*.qcow2", "QEMU disk image"},
		{"*.img", "raw disk image"},
		{"*.iso", "optical disc image, usually re-downloadable"},
		{"*.dmg", "macOS disk image, usually re-downloadable"},
		{"*.hds", "Parallels disk image"},
		{"*.pvm/", "Parallels virtual machine bundle"},
		{"*.vmwarevm/", "VMware virtual machine bundle"},
		{"VirtualBox VMs/", "VirtualBox machine directory"},
		{"wsl/", "WSL distribution images"},
		{"docker-desktop-data/", "Docker Desktop virtual disk"},
		{"*.vswap", "virtual machine swap"},
		{"*.avhdx", "Hyper-V differencing disk"},
	}
}

// ephemeralRules exclude things that are meaningless outside the running
// machine: sockets, pipes, mounted network drives, and OS sync scratch areas.
func ephemeralRules() []Rule {
	return anyDepth([]Rule{
		{"*.sock", "unix socket"},
		{"*.pid", "process id file"},
		{"*.lock", "lock file"},
		{".sync/", "sync client scratch data"},
		{".dropbox.cache/", "Dropbox cache"},
		{".dropbox/", "Dropbox client state"},
		{"*.icloud", "iCloud placeholder for an evicted file"},
		{"AppData/Roaming/Microsoft/Windows/Recent/", "recent items list"},
		{"AppData/Local/D3DSCache/", "DirectX shader cache"},
		{"Steam/steamapps/", "game installs, redownloadable from Steam"},
		{"steamapps/", "game installs, redownloadable from Steam"},
		{"Epic Games/", "game installs, redownloadable"},
		{"Battle.net/", "game installs, redownloadable"},
		{"Riot Games/", "game installs, redownloadable"},
	})
}

// DefaultRuleSet returns every default rule grouped by category for the
// current platform.
func DefaultRuleSet() map[Category][]Rule {
	return map[Category][]Rule{
		CategorySystem:     systemRoots(runtime.GOOS),
		CategoryJunk:       junkRules(),
		CategoryCache:      cacheRules(),
		CategoryDeveloper:  append(developerGlobalRules(), developerScopedRules()...),
		CategoryVirtualise: virtualisationRules(),
		CategoryEphemeral:  ephemeralRules(),
	}
}

// DefaultCategories lists the categories enabled out of the box. All of them
// are, which is what makes the product zero-configuration.
func DefaultCategories() []Category {
	return []Category{CategorySystem, CategoryJunk, CategoryCache, CategoryDeveloper, CategoryVirtualise, CategoryEphemeral}
}

// SystemRootPaths returns the absolute paths that are always off-limits,
// cleaned for the current platform.
func SystemRootPaths() []Rule {
	rules := systemRoots(runtime.GOOS)
	out := make([]Rule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		clean := normalizeAbs(r.Pattern)
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, Rule{Pattern: clean, Reason: r.Reason})
	}
	return out
}

// normalizeAbs canonicalises an absolute path for prefix comparison: forward
// slashes everywhere, no trailing separator, lower-cased on case-insensitive
// platforms.
func normalizeAbs(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	if caseInsensitiveFS() {
		p = strings.ToLower(p)
	}
	return p
}

// caseInsensitiveFS reports whether path comparisons should fold case.
func caseInsensitiveFS() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}
