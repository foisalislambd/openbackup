#!/bin/sh
# OpenBackup agent installer for Linux and macOS.
#
# It is served by your own server, so the download URL and the address the agent
# will talk to are the same host you already trust:
#
#   curl -fsSL https://backup.example.com/install.sh | sh
#
# The script installs one static binary, registers a background service, and does
# nothing else. It never touches system files, and it backs up nothing until you
# connect the device with a one-time code from the dashboard.
set -eu

SERVER_URL="${OPENBACKUP_SERVER:-__SERVER_URL__}"
VERSION="${OPENBACKUP_VERSION:-__VERSION__}"
RELEASES="${OPENBACKUP_RELEASES:-https://github.com/foisalislambd/openbackup/releases}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }

# ---------------------------------------------------------------------------
# Work out what to download
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
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
else
	die "curl or wget is required"
fi

if [ "$VERSION" = "dev" ] || [ -z "$VERSION" ]; then
	url="${RELEASES}/latest/download/openbackup-${goos}-${goarch}"
else
	url="${RELEASES}/download/${VERSION}/openbackup-${goos}-${goarch}"
fi

tmp=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp'" EXIT INT TERM

say "Downloading the OpenBackup agent (${goos}/${goarch})..."
fetch "$url" "$tmp/openbackup" || die "could not download $url"
chmod +x "$tmp/openbackup"

# Verify it runs before replacing anything that is already installed.
"$tmp/openbackup" version >/dev/null 2>&1 || die "the downloaded binary does not run on this machine"

# mv across filesystems can fail, and an in-place overwrite of a running binary
# fails on some systems, so copy to a temporary name next to the target and
# rename: on the same filesystem that swap is atomic.
cp "$tmp/openbackup" "$bindir/.openbackup.new"
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
