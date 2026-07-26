#!/bin/sh
# Install the OpenBackup desktop app on Linux (same window as Windows).
#
# Prefer a release build when one exists; otherwise build on this machine
# (needs Go, Node, Wails, and WebKitGTK).
#
#   curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-desktop-linux.sh | sh
#
# Or, after `make desktop` in a clone:
#   ./scripts/install-desktop-linux.sh
#
set -eu

RELEASES="${OPENBACKUP_RELEASES:-https://github.com/foisalislambd/openbackup/releases}"
REPO="${OPENBACKUP_REPO:-https://github.com/foisalislambd/openbackup.git}"
REF="${OPENBACKUP_REF:-main}"
GO_VERSION="${OPENBACKUP_GO_VERSION:-1.26.5}"
WAILS_VERSION="${OPENBACKUP_WAILS_VERSION:-v2.13.0}"
PREFIX="${OPENBACKUP_PREFIX:-$HOME/.local}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
need() { have "$1" || die "$1 is required but not installed"; }

os=$(uname -s)
[ "$os" = Linux ] || die "this installer is for Linux (on Windows use the Releases installer)"

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) goarch=amd64 ;;
	aarch64 | arm64) goarch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

bindir="$PREFIX/bin"
appdir="$PREFIX/share/openbackup"
applications="$PREFIX/share/applications"
icons="$PREFIX/share/icons/hicolor/256x256/apps"
mkdir -p "$bindir" "$appdir" "$applications" "$icons"

if have curl; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif have wget; then
	fetch() { wget -qO "$2" "$1"; }
else
	die "curl or wget is required"
fi

tmp=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp'" EXIT INT TERM

# ---------------------------------------------------------------------------
# Agent CLI (desktop drives service install through it)
# ---------------------------------------------------------------------------

ensure_agent() {
	if [ -x "$bindir/openbackup" ] || have openbackup; then
		say "Agent CLI already present."
		return 0
	fi
	say "Installing the OpenBackup agent CLI..."
	agent_url="${RELEASES}/latest/download/openbackup-linux-${goarch}"
	if fetch "$agent_url" "$tmp/openbackup" 2>/dev/null; then
		chmod 755 "$tmp/openbackup"
		mv "$tmp/openbackup" "$bindir/openbackup"
		say "Installed $bindir/openbackup"
		return 0
	fi
	say "No release agent binary yet — installing via install-agent.sh..."
	fetch "https://raw.githubusercontent.com/foisalislambd/openbackup/${REF}/scripts/install-agent.sh" "$tmp/install-agent.sh"
	sh "$tmp/install-agent.sh"
}

# ---------------------------------------------------------------------------
# Desktop binary: release download, local build/bin, or build from git
# ---------------------------------------------------------------------------

try_release_desktop() {
	url="${RELEASES}/latest/download/openbackup-desktop-linux-${goarch}"
	say "Trying release desktop binary..."
	if fetch "$url" "$tmp/openbackup-desktop" 2>/dev/null; then
		chmod 755 "$tmp/openbackup-desktop"
		"$tmp/openbackup-desktop" -h >/dev/null 2>&1 || true
		return 0
	fi
	rm -f "$tmp/openbackup-desktop"
	return 1
}

try_local_desktop() {
	# Running from a clone after `make desktop`.
	here=$(CDPATH= cd -- "$(dirname "$0")" 2>/dev/null && pwd) || return 1
	candidate="$here/../desktop/build/bin/openbackup-desktop"
	if [ -x "$candidate" ]; then
		cp "$candidate" "$tmp/openbackup-desktop"
		chmod 755 "$tmp/openbackup-desktop"
		say "Using local build at $candidate"
		return 0
	fi
	return 1
}

ensure_build_deps() {
	need git
	if ! have go; then
		need tar
		gotar="go${GO_VERSION}.linux-${goarch}.tar.gz"
		say "Go not found; downloading Go ${GO_VERSION}..."
		fetch "https://go.dev/dl/${gotar}" "$tmp/${gotar}"
		tar -C "$tmp" -xzf "$tmp/${gotar}"
		export PATH="$tmp/go/bin:$PATH"
		export GOTOOLCHAIN=local
	fi
	have go || die "Go is required to build the desktop app"
	if ! have node || ! have npm; then
		die "Node.js and npm are required to build the desktop UI (install Node 22+)"
	fi
	if ! have pkg-config; then
		die "pkg-config is required (install build tools for WebKitGTK)"
	fi
	if ! pkg-config --exists webkit2gtk-4.1 2>/dev/null && ! pkg-config --exists webkit2gtk-4.0 2>/dev/null; then
		die "WebKitGTK headers missing. On Ubuntu/Debian:
  sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev
On Fedora:
  sudo dnf install gtk3-devel webkit2gtk4.1-devel"
	fi
	if ! have wails; then
		say "Installing Wails CLI ${WAILS_VERSION}..."
		go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
		export PATH="$(go env GOPATH)/bin:$PATH"
	fi
	have wails || die "could not install the Wails CLI"
}

build_desktop() {
	ensure_build_deps
	say "Cloning ${REPO} (${REF})..."
	if ! git clone --depth 1 --branch "$REF" "$REPO" "$tmp/src" 2>/dev/null; then
		rm -rf "$tmp/src"
		git clone --depth 1 "$REPO" "$tmp/src" || die "could not clone $REPO"
		git -C "$tmp/src" fetch --depth 1 origin "$REF" 2>/dev/null || true
		git -C "$tmp/src" checkout "$REF" || die "could not check out $REF"
	fi
	say "Building the desktop app (first time can take a few minutes)..."
	(
		cd "$tmp/src"
		make desktop
	) || die "desktop build failed"
	cp "$tmp/src/desktop/build/bin/openbackup-desktop" "$tmp/openbackup-desktop"
	chmod 755 "$tmp/openbackup-desktop"
}

install_desktop_files() {
	mv "$tmp/openbackup-desktop" "$bindir/openbackup-desktop"
	# Keep a copy of the icon next to the app for the .desktop file.
	if [ -f "$tmp/src/desktop/build/appicon.png" ]; then
		cp "$tmp/src/desktop/build/appicon.png" "$icons/openbackup.png"
		cp "$tmp/src/desktop/build/appicon.png" "$appdir/appicon.png"
	elif [ -f "$(dirname "$0")/../desktop/build/appicon.png" ]; then
		cp "$(dirname "$0")/../desktop/build/appicon.png" "$icons/openbackup.png"
		cp "$(dirname "$0")/../desktop/build/appicon.png" "$appdir/appicon.png"
	fi

	desktop_file="$applications/openbackup-desktop.desktop"
	cat >"$desktop_file" <<EOF
[Desktop Entry]
Type=Application
Version=1.0
Name=OpenBackup
GenericName=Backup
Comment=Automatic backup for your files
Exec=$bindir/openbackup-desktop
Icon=openbackup
Terminal=false
Categories=Utility;Archiving;
StartupWMClass=OpenBackup
Keywords=backup;restore;files;
EOF
	chmod 644 "$desktop_file"

	if have update-desktop-database; then
		update-desktop-database "$applications" >/dev/null 2>&1 || true
	fi
	if have gtk-update-icon-cache && [ -d "$PREFIX/share/icons/hicolor" ]; then
		gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" >/dev/null 2>&1 || true
	fi
}

ensure_agent

if try_release_desktop || try_local_desktop; then
	:
else
	say "No packaged desktop binary found — building from source..."
	build_desktop
fi

install_desktop_files

case ":$PATH:" in
	*":$bindir:"*) ;;
	*)
		say ""
		say "Add $bindir to your PATH:"
		say "  export PATH=\"$bindir:\$PATH\""
		;;
esac

say ""
say "Installed OpenBackup desktop:"
say "  $bindir/openbackup-desktop"
say "  $applications/openbackup-desktop.desktop"
say ""
say "Open it from your app menu, or run:"
say "  openbackup-desktop"
say ""
say "First launch walks you through connecting with a dashboard code —"
say "same flow as the Windows app."
