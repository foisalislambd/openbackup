#!/bin/sh
# OpenBackup agent installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
#   openbackup connect --server https://YOUR-SERVER --code YOUR-CODE
#
# Prefers a GitHub release binary; if none exists, builds from git (downloads a
# temporary Go toolchain if needed). Installs one binary and a background
# service. Nothing is backed up until you connect with a dashboard code.
set -eu

VERSION="${OPENBACKUP_VERSION:-}"
RELEASES="${OPENBACKUP_RELEASES:-https://github.com/foisalislambd/openbackup/releases}"
REPO="${OPENBACKUP_REPO:-https://github.com/foisalislambd/openbackup.git}"
REF="${OPENBACKUP_REF:-main}"
GO_VERSION="${OPENBACKUP_GO_VERSION:-1.26.5}"
FORCE_BUILD="${OPENBACKUP_FORCE_BUILD:-0}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }
have() { command -v "$1" >/dev/null 2>&1; }

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

# Root → system-wide. Otherwise → per-user (better for personal machines).
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

if [ -n "$VERSION" ]; then
	url="${RELEASES}/download/${VERSION}/openbackup-${goos}-${goarch}"
else
	url="${RELEASES}/latest/download/openbackup-${goos}-${goarch}"
fi

tmp=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp'" EXIT INT TERM
bin="$tmp/openbackup"

try_download() {
	say "Downloading the OpenBackup agent (${goos}/${goarch})..."
	if ! fetch "$url" "$bin"; then
		say "Release binary not available at $url"
		rm -f "$bin"
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
	if ! git clone --depth 1 --branch "$REF" "$REPO" "$tmp/src" 2>/dev/null; then
		rm -rf "$tmp/src"
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

cp "$bin" "$bindir/.openbackup.new"
chmod 755 "$bindir/.openbackup.new"
mv "$bindir/.openbackup.new" "$bindir/openbackup"
ob="$bindir/openbackup"
say "Installed $ob"

case ":$PATH:" in
	*":$bindir:"*) ;;
	*)
		say ""
		say "Add $bindir to your PATH (run this now, and put it in your shell profile):"
		say "  export PATH=\"$bindir:\$PATH\""
		;;
esac

if "$ob" service install >/dev/null 2>&1; then
	say "Registered the background service ($scope)."
else
	say "Could not register the background service automatically."
	say "Run '$ob service install' yourself, or start it manually with '$ob run'."
fi

say ""
say "Installed. Nothing is being backed up yet."
say ""
say "Connect this device using the one-time code from your dashboard:"
say "  $ob connect --server https://YOUR-SERVER --code YOUR-CODE"
say ""
say "Then '$ob status' shows what it is doing, and '$ob folders' shows"
say "which folders it found."
