package ignore

import (
	"path"
	"strings"
)

// ProjectKind identifies a detected source-code project.
type ProjectKind string

// Detected project kinds.
const (
	ProjectNode      ProjectKind = "node"
	ProjectGo        ProjectKind = "go"
	ProjectRust      ProjectKind = "rust"
	ProjectPython    ProjectKind = "python"
	ProjectJava      ProjectKind = "java"
	ProjectGradle    ProjectKind = "gradle"
	ProjectDotNet    ProjectKind = "dotnet"
	ProjectPHP       ProjectKind = "php"
	ProjectRuby      ProjectKind = "ruby"
	ProjectDart      ProjectKind = "dart"
	ProjectElixir    ProjectKind = "elixir"
	ProjectSwift     ProjectKind = "swift"
	ProjectCMake     ProjectKind = "cmake"
	ProjectTerraform ProjectKind = "terraform"
	ProjectGit       ProjectKind = "git"
	ProjectUnity     ProjectKind = "unity"
	ProjectUnreal    ProjectKind = "unreal"
)

// projectMarker maps a marker file to the project kind it identifies and the
// generated directories that can be rebuilt from the committed sources.
type projectMarker struct {
	kind ProjectKind
	// dirs are directory names, relative to the project root, that are safe to
	// skip once the marker is present.
	dirs []string
	// reason explains the exclusion in the UI.
	reason string
}

// markers is the smart-detection table. Detection is per directory: a folder
// containing package.json is a Node project, so node_modules beneath it is
// dependency output rather than user data.
var markers = map[string]projectMarker{
	"package.json":          {ProjectNode, []string{"node_modules", ".next", ".nuxt", ".svelte-kit", ".astro", ".angular", ".parcel-cache", ".turbo", ".vite", "dist", "build", "out", "coverage", ".nyc_output"}, "Node.js project: dependencies and build output are reproducible with a package install"},
	"pnpm-lock.yaml":        {ProjectNode, []string{"node_modules", ".pnpm-store"}, "pnpm project: dependencies are reproducible from the lockfile"},
	"yarn.lock":             {ProjectNode, []string{"node_modules", ".yarn/cache", ".yarn/unplugged"}, "Yarn project: dependencies are reproducible from the lockfile"},
	"bun.lockb":             {ProjectNode, []string{"node_modules"}, "Bun project: dependencies are reproducible from the lockfile"},
	"deno.json":             {ProjectNode, []string{"node_modules"}, "Deno project: dependencies are cached, not source"},
	"go.mod":                {ProjectGo, []string{"vendor", "bin", "dist"}, "Go module: vendored dependencies and binaries are reproducible with go build"},
	"Cargo.toml":            {ProjectRust, []string{"target"}, "Cargo project: target/ is build output"},
	"pyproject.toml":        {ProjectPython, []string{".venv", "venv", "dist", "build", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox", "*.egg-info"}, "Python project: virtual environments and caches are recreatable"},
	"requirements.txt":      {ProjectPython, []string{".venv", "venv", "__pycache__", ".pytest_cache"}, "Python project: virtual environments are recreatable with pip install"},
	"Pipfile":               {ProjectPython, []string{".venv", "__pycache__"}, "Pipenv project: the virtual environment is recreatable"},
	"setup.py":              {ProjectPython, []string{".venv", "venv", "build", "dist", "__pycache__", "*.egg-info"}, "Python project: build artefacts are recreatable"},
	"pom.xml":               {ProjectJava, []string{"target"}, "Maven project: target/ is build output"},
	"build.gradle":          {ProjectGradle, []string{"build", ".gradle", "app/build"}, "Gradle project: build/ and .gradle/ are build state"},
	"build.gradle.kts":      {ProjectGradle, []string{"build", ".gradle", "app/build"}, "Gradle project: build/ and .gradle/ are build state"},
	"composer.json":         {ProjectPHP, []string{"vendor"}, "Composer project: vendor/ is reproducible with composer install"},
	"Gemfile":               {ProjectRuby, []string{"vendor/bundle", ".bundle", "tmp", "log"}, "Bundler project: gems are reproducible with bundle install"},
	"pubspec.yaml":          {ProjectDart, []string{".dart_tool", "build", ".flutter-plugins"}, "Dart/Flutter project: build state is recreatable"},
	"mix.exs":               {ProjectElixir, []string{"_build", "deps"}, "Elixir project: deps and _build are reproducible with mix deps.get"},
	"Package.swift":         {ProjectSwift, []string{".build", "Packages"}, "Swift package: .build is build output"},
	"CMakeLists.txt":        {ProjectCMake, []string{"build", "cmake-build-debug", "cmake-build-release", "CMakeFiles", "out"}, "CMake project: build directories are regenerable"},
	"Makefile":              {ProjectCMake, []string{}, "Make project"},
	"main.tf":               {ProjectTerraform, []string{".terraform"}, "Terraform project: provider plugins are re-downloadable"},
	"Gopkg.toml":            {ProjectGo, []string{"vendor"}, "Go dep project: vendor/ is reproducible"},
	"ProjectSettings":       {ProjectUnity, []string{"Library", "Temp", "Obj", "Build", "Builds", "Logs"}, "Unity project: Library/ and build folders are regenerated from Assets"},
	"gradle.properties":     {ProjectGradle, []string{"build", ".gradle"}, "Gradle project: build state is regenerable"},
	"Directory.Build.props": {ProjectDotNet, []string{"bin", "obj", "packages"}, ".NET project: bin/ and obj/ are build output"},
}

// suffixMarkers matches marker files by extension, for ecosystems where the
// project file is named after the project (for example Foo.csproj).
var suffixMarkers = map[string]projectMarker{
	".csproj":    {ProjectDotNet, []string{"bin", "obj", "packages"}, ".NET project: bin/ and obj/ are build output"},
	".fsproj":    {ProjectDotNet, []string{"bin", "obj"}, ".NET project: bin/ and obj/ are build output"},
	".vbproj":    {ProjectDotNet, []string{"bin", "obj"}, ".NET project: bin/ and obj/ are build output"},
	".sln":       {ProjectDotNet, []string{"bin", "obj", ".vs", "packages"}, ".NET solution: build output and IDE state"},
	".xcodeproj": {ProjectSwift, []string{"build", "DerivedData", "Pods"}, "Xcode project: build output and CocoaPods checkout"},
	".uproject":  {ProjectUnreal, []string{"Binaries", "Build", "DerivedDataCache", "Intermediate", "Saved"}, "Unreal project: intermediate and derived data are regenerable"},
}

// Project describes a detected project rooted at a directory.
type Project struct {
	// Root is the project directory, relative to the backup root, slash separated.
	Root string
	Kind ProjectKind
	// Marker is the file that triggered detection.
	Marker string
	// Excluded lists the relative directories skipped inside this project.
	Excluded []string
	Reason   string
}

// DetectProject inspects the entry names of a directory and reports the project
// rooted there, if any. names should be the immediate children of dir.
func DetectProject(dir string, names []string) *Project {
	var best *Project
	for _, name := range names {
		if m, ok := markers[name]; ok {
			p := &Project{Root: dir, Kind: m.kind, Marker: name, Excluded: m.dirs, Reason: m.reason}
			best = mergeProject(best, p)
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if m, ok := suffixMarkers[ext]; ok {
			p := &Project{Root: dir, Kind: m.kind, Marker: name, Excluded: m.dirs, Reason: m.reason}
			best = mergeProject(best, p)
		}
	}
	return best
}

// mergeProject combines multiple markers in one directory (a Node + Go polyglot
// repo, say) so all of their generated directories are excluded.
func mergeProject(existing, next *Project) *Project {
	if existing == nil {
		return next
	}
	seen := make(map[string]struct{}, len(existing.Excluded))
	for _, d := range existing.Excluded {
		seen[d] = struct{}{}
	}
	for _, d := range next.Excluded {
		if _, ok := seen[d]; !ok {
			existing.Excluded = append(existing.Excluded, d)
			seen[d] = struct{}{}
		}
	}
	existing.Marker += ", " + next.Marker
	return existing
}

// MarkerNames returns every recognised marker filename, for documentation and
// tests.
func MarkerNames() []string {
	out := make([]string, 0, len(markers)+len(suffixMarkers))
	for name := range markers {
		out = append(out, name)
	}
	for ext := range suffixMarkers {
		out = append(out, "*"+ext)
	}
	return out
}
