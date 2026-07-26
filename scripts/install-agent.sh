#!/bin/sh
# OpenBackup agent installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
#
# Prefers a GitHub release binary; if none exists, builds the agent from git.
# On Linux it also downloads the desktop app when a release binary exists
# (same window as Windows). Skip with OPENBACKUP_SKIP_DESKTOP=1.
#
# Nothing is backed up until you connect with a dashboard code (or open the app).
set -eu

VERSION="${OPENBACKUP_VERSION:-}"
RELEASES="${OPENBACKUP_RELEASES:-https://github.com/foisalislambd/openbackup/releases}"
REPO="${OPENBACKUP_REPO:-https://github.com/foisalislambd/openbackup.git}"
REF="${OPENBACKUP_REF:-main}"
GO_VERSION="${OPENBACKUP_GO_VERSION:-1.26.5}"
FORCE_BUILD="${OPENBACKUP_FORCE_BUILD:-0}"
SKIP_DESKTOP="${OPENBACKUP_SKIP_DESKTOP:-0}"
PREFIX="${OPENBACKUP_PREFIX:-}"

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
	bindir="${PREFIX:-/usr/local}/bin"
	datadir="${PREFIX:-/usr/local}/share"
	scope=system
else
	home_prefix="${PREFIX:-$HOME/.local}"
	bindir="$home_prefix/bin"
	datadir="$home_prefix/share"
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
	agent_url="${RELEASES}/download/${VERSION}/openbackup-${goos}-${goarch}"
	desktop_url="${RELEASES}/download/${VERSION}/openbackup-desktop-${goos}-${goarch}"
	icon_url="${RELEASES}/download/${VERSION}/openbackup.png"
	desktop_entry_url="${RELEASES}/download/${VERSION}/openbackup-desktop.desktop"
else
	agent_url="${RELEASES}/latest/download/openbackup-${goos}-${goarch}"
	desktop_url="${RELEASES}/latest/download/openbackup-desktop-${goos}-${goarch}"
	icon_url="${RELEASES}/latest/download/openbackup.png"
	desktop_entry_url="${RELEASES}/latest/download/openbackup-desktop.desktop"
fi

tmp=$(mktemp -d)
# shellcheck disable=SC2064
trap "rm -rf '$tmp'" EXIT INT TERM
bin="$tmp/openbackup"

try_download() {
	say "Downloading the OpenBackup agent (${goos}/${goarch})..."
	if ! fetch "$agent_url" "$bin"; then
		say "Release binary not available at $agent_url"
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

# On Linux, also install the desktop app from Releases when available.
install_desktop_linux() {
	[ "$goos" = linux ] || return 0
	[ "$SKIP_DESKTOP" = "1" ] && {
		say "OPENBACKUP_SKIP_DESKTOP=1 — skipping desktop app"
		return 0
	}

	desk="$tmp/openbackup-desktop"
	say "Looking for Linux desktop app (${goarch})..."
	if ! fetch "$desktop_url" "$desk" 2>/dev/null; then
		say "No desktop release binary yet — agent CLI only. Re-run later after a desktop release."
		rm -f "$desk"
		return 0
	fi
	chmod 755 "$desk"

	cp "$desk" "$bindir/.openbackup-desktop.new"
	mv "$bindir/.openbackup-desktop.new" "$bindir/openbackup-desktop"
	say "Installed $bindir/openbackup-desktop"

	applications="$datadir/applications"
	icons="$datadir/icons/hicolor/256x256/apps"
	mkdir -p "$applications" "$icons" "$datadir/openbackup"

	if fetch "$icon_url" "$tmp/openbackup.png" 2>/dev/null; then
		cp "$tmp/openbackup.png" "$icons/openbackup.png"
		cp "$tmp/openbackup.png" "$datadir/openbackup/appicon.png"
	fi

	desktop_file="$applications/openbackup-desktop.desktop"
	if fetch "$desktop_entry_url" "$tmp/entry.desktop" 2>/dev/null; then
		# Point Exec at the path we actually installed.
		sed "s|^Exec=.*|Exec=$bindir/openbackup-desktop|" "$tmp/entry.desktop" >"$desktop_file"
	else
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
	fi
	chmod 644 "$desktop_file"

	if have update-desktop-database; then
		update-desktop-database "$applications" >/dev/null 2>&1 || true
	fi

	# Runtime WebKitGTK is required to open the window.
	if ! ldconfig -p 2>/dev/null | grep -q 'libwebkit2gtk-4\.1\.so'; then
		if [ ! -e /usr/lib/x86_64-linux-gnu/libwebkit2gtk-4.1.so.0 ] &&
			[ ! -e /usr/lib/aarch64-linux-gnu/libwebkit2gtk-4.1.so.0 ] &&
			[ ! -e /usr/lib64/libwebkit2gtk-4.1.so.0 ]; then
			say ""
			say "Desktop app needs WebKitGTK 4.1. Install then open openbackup-desktop:"
			if have apt-get; then
				say "  sudo apt-get install -y libwebkit2gtk-4.1-0 libgtk-3-0"
			elif have dnf; then
				say "  sudo dnf install -y webkit2gtk4.1 gtk3"
			elif have pacman; then
				say "  sudo pacman -S webkit2gtk-4.1 gtk3"
			else
				say "  (install your distro's webkit2gtk 4.1 package)"
			fi
		fi
	fi
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

install_desktop_linux

say ""
say "Installed. Nothing is being backed up yet."
say ""
if [ -x "$bindir/openbackup-desktop" ]; then
	say "Open the app (same as Windows):"
	say "  openbackup-desktop"
	say ""
	say "Or connect from the terminal:"
else
	say "Connect this device using the one-time code from your dashboard:"
fi
say "  $ob connect --server https://YOUR-SERVER --code YOUR-CODE"
say ""
say "Then '$ob status' shows what it is doing, and '$ob folders' shows"
say "which folders it found."
