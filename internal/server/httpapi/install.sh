#!/bin/sh
# OpenBackup agent installer for Linux and macOS.
#
# It is served by your own server, so the download URL and the address the agent
# will talk to are the same host you already trust:
#
#   curl -fsSL https://backup.example.com/install.sh | sh
#
# Prefers a published GitHub release binary. If none exists yet (or the download
# fails), it clones the repo and builds the agent locally — same idea as the
# server installer falling back to a source build.
#
# The script installs one static binary, registers a background service, and does
# nothing else. It never touches system files, and it backs up nothing until you
# connect the device with a one-time code from the dashboard.
set -eu

SERVER_URL="${OPENBACKUP_SERVER:-__SERVER_URL__}"
VERSION="${OPENBACKUP_VERSION:-__VERSION__}"
RELEASES="${OPENBACKUP_RELEASES:-https://github.com/foisalislambd/openbackup/releases}"
REPO="${OPENBACKUP_REPO:-https://github.com/foisalislambd/openbackup.git}"
REF="${OPENBACKUP_REF:-main}"
# Pin a Go toolchain for source builds when `go` is not already installed.
GO_VERSION="${OPENBACKUP_GO_VERSION:-1.26.5}"
FORCE_BUILD="${OPENBACKUP_FORCE_BUILD:-0}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }
have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------------------
# Work out what to download / build for
# ---------------------------------------------------------------------------

os=$(uname -s)
case "$os" in
	Linux) goos=linux ;;
	Darwin) goos=darwin ;;
	*) die "unsupported operating system: $os (Windows users: download the installer from $RELEASES)" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) goarch=amd64 ;;
	aarch64 | arm64) goarch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

# ---------------------------------------------------------------------------
# Decide where it goes
# ---------------------------------------------------------------------------

# Root installs system-wide and runs the agent as a system service. Without root
# the agent installs per-user, which is the better fit anyway: it backs up your
# files, so it only needs your permissions.
if [ "$(id -u)" = "0" ]; then
	bindir=/usr/local/bin
	scope=system
else
	bindir="${HOME}/.local/bin"
	scope=user
fi
mkdir -p "$bindir"

need uname
need mkdir
if have curl; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif have wget; then
	fetch() { wget -qO "$2" "$1"; }
else
	die "curl or wget is required"
fi

if [ "$VERSION" = "dev" ] || [ -z "$VERSION" ] || [ "$VERSION" = "__VERSION__" ]; then
	url="${RELEASES}/latest/download/openbackup-${goos}-${goarch}"
else
	url="${RELEASES}/download/${VERSION}/openbackup-${goos}-${goarch}"
fi

tmp=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp'" EXIT INT TERM
bin="$tmp/openbackup"

# ---------------------------------------------------------------------------
# Obtain the binary: release download, else build from git
# ---------------------------------------------------------------------------

try_download() {
	say "Downloading the OpenBackup agent (${goos}/${goarch})..."
	if ! fetch "$url" "$bin"; then
		say "Release binary not available at $url"
		return 1
	fi
	chmod +x "$bin"
	if ! "$bin" version >/dev/null 2>&1; then
		say "Downloaded file is not a working agent binary"
		rm -f "$bin"
		return 1
	fi
	return 0
}

# Download an official Go toolchain into $tmp so a machine without Go can still
# build from source. Prefer a system Go when present.
ensure_go() {
	if have go; then
		return 0
	fi

	need tar
	gotar="go${GO_VERSION}.${goos}-${goarch}.tar.gz"
	gourl="https://go.dev/dl/${gotar}"
	say "Go not found; downloading a temporary Go ${GO_VERSION} toolchain..."
	fetch "$gourl" "$tmp/${gotar}" || die "could not download Go from $gourl — install Go ${GO_VERSION}+ and re-run"
	tar -C "$tmp" -xzf "$tmp/${gotar}"
	export PATH="$tmp/go/bin:$PATH"
	export GOTOOLCHAIN=local
	have go || die "temporary Go toolchain failed to install"
	say "Using temporary Go $(go env GOVERSION)"
}

build_from_git() {
	need git
	ensure_go

	say "Cloning ${REPO} (${REF})..."
	# --branch accepts branch or tag names. Depth 1 keeps the clone small.
	if ! git clone --depth 1 --branch "$REF" "$REPO" "$tmp/src" 2>/dev/null; then
		# Some refs need a full fetch (e.g. a commit SHA). Fall back.
		git clone --depth 1 "$REPO" "$tmp/src" || die "could not clone $REPO"
		git -C "$tmp/src" fetch --depth 1 origin "$REF" 2>/dev/null || true
		git -C "$tmp/src" checkout "$REF" || die "could not check out $REF"
	fi

	say "Building the agent (first time can take a minute)..."
	(
		cd "$tmp/src"
		CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o "$bin" ./cmd/openbackup
	) || die "go build failed — need network access to module proxies, or install Go ${GO_VERSION}+ yourself"
	chmod +x "$bin"
	"$bin" version >/dev/null 2>&1 || die "the built binary does not run on this machine"
	say "Built from source"
}

if [ "$FORCE_BUILD" = "1" ]; then
	say "OPENBACKUP_FORCE_BUILD=1 — skipping release download"
	build_from_git
elif ! try_download; then
	say "Falling back to building from git..."
	build_from_git
fi

# mv across filesystems can fail, and an in-place overwrite of a running binary
# fails on some systems, so copy to a temporary name next to the target and
# rename: on the same filesystem that swap is atomic.
cp "$bin" "$bindir/.openbackup.new"
chmod 755 "$bindir/.openbackup.new"
mv "$bindir/.openbackup.new" "$bindir/openbackup"
say "Installed $bindir/openbackup"

case ":$PATH:" in
	*":$bindir:"*) ;;
	*) say ""; say "Note: $bindir is not in your PATH. Add this to your shell profile:"; say "  export PATH=\"$bindir:\$PATH\"" ;;
esac

# ---------------------------------------------------------------------------
# Background service
# ---------------------------------------------------------------------------

if "$bindir/openbackup" service install >/dev/null 2>&1; then
	say "Registered the background service ($scope)."
else
	say "Could not register the background service automatically."
	say "Run '$bindir/openbackup service install' yourself, or start it manually with '$bindir/openbackup run'."
fi

# ---------------------------------------------------------------------------
# What to do next
# ---------------------------------------------------------------------------

say ""
say "Installed. Nothing is being backed up yet."
say ""
say "Connect this device using the one-time code from your dashboard:"
if [ -n "$SERVER_URL" ] && [ "$SERVER_URL" != "__SERVER_URL__" ]; then
	say "  openbackup connect --server $SERVER_URL --code YOUR-CODE"
	say ""
	say "Get a code at $SERVER_URL (Devices > Create connection code)."
else
	say "  openbackup connect --server https://your-server --code YOUR-CODE"
fi
say ""
say "Then 'openbackup status' shows what it is doing, and 'openbackup folders' shows"
say "which folders it found. It backs up your personal folders only, and skips"
say "system files, caches and build output."
