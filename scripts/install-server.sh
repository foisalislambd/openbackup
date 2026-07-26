#!/bin/sh
# OpenBackup server installer for a VPS.
#
#   curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-server.sh | sudo sh
#
# For someone who has never used Docker: this script installs whatever is
# missing (curl, git, Docker, Compose), pulls or builds the server image,
# starts it, and prints the dashboard address and login. The only thing you
# type is that one line.
#
# Optional environment variables (all have working defaults):
#   OPENBACKUP_PUBLIC_URL   https://backup.example.com  (set this for real use)
#   OPENBACKUP_PORT         18200
#   OPENBACKUP_ADMIN_EMAIL  admin@localhost
#   OPENBACKUP_IMAGE        foisalislambd/openbackup:latest
#   OPENBACKUP_DIR          /opt/openbackup
#
# Default port is 18200 (not 8080) so it is less likely to clash with other
# apps on a typical VPS. Override with OPENBACKUP_PORT if needed.
#
set -eu

DIR="${OPENBACKUP_DIR:-/opt/openbackup}"
IMAGE="${OPENBACKUP_IMAGE:-foisalislambd/openbackup:latest}"
REPO="${OPENBACKUP_REPO:-https://github.com/foisalislambd/openbackup.git}"
REF="${OPENBACKUP_REF:-main}"
PORT="${OPENBACKUP_PORT:-18200}"
PUBLIC_URL="${OPENBACKUP_PUBLIC_URL:-}"
ADMIN_EMAIL="${OPENBACKUP_ADMIN_EMAIL:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Must be root: we may install Docker and open a firewall port
# ---------------------------------------------------------------------------

if [ "$(id -u)" != "0" ]; then
	die "run as root so missing tools can be installed automatically:

  curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-server.sh | sudo sh"
fi

# ---------------------------------------------------------------------------
# Tiny helpers for package installs across common VPS distros
# ---------------------------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

pkg_install() {
	# $@ = package names. Quiet where the manager allows it; failures are fatal
	# because the caller only asks for things that are required next.
	if have apt-get; then
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq
		apt-get install -y -qq "$@"
	elif have dnf; then
		dnf install -y "$@"
	elif have yum; then
		yum install -y "$@"
	elif have apk; then
		apk add --no-cache "$@"
	elif have zypper; then
		zypper --non-interactive install "$@"
	else
		die "cannot install packages automatically on this OS; install $* by hand and re-run"
	fi
}

ensure_cmd() {
	# ensure_cmd curl curl ca-certificates
	# First arg is the binary to check; the rest are packages to install if missing.
	bin=$1
	shift
	have "$bin" && return 0
	say "Installing $*..."
	pkg_install "$@"
	have "$bin" || die "installed $* but still cannot find $bin"
}

fetch() {
	# Download a URL to stdout. Prefer curl; fall back to wget.
	url=$1
	if have curl; then
		curl -fsSL "$url"
	elif have wget; then
		wget -qO- "$url"
	else
		die "curl or wget is required"
	fi
}

# ---------------------------------------------------------------------------
# Base tools every step below needs
# ---------------------------------------------------------------------------

say "Checking the machine..."
ensure_cmd curl curl ca-certificates
# openssl is nice for password generation; not fatal if missing.
have openssl || pkg_install openssl 2>/dev/null || true

# ---------------------------------------------------------------------------
# Docker Engine + Compose
# ---------------------------------------------------------------------------

install_docker() {
	say "Docker is not installed. Installing it now (official installer)..."
	# get.docker.com covers Ubuntu, Debian, Fedora, CentOS, Raspberry Pi OS, etc.
	fetch https://get.docker.com | sh
	have docker || die "Docker install finished but the docker command is missing"
}

start_docker() {
	if have systemctl; then
		systemctl enable docker >/dev/null 2>&1 || true
		systemctl start docker >/dev/null 2>&1 || true
	elif have service; then
		service docker start >/dev/null 2>&1 || true
	fi
	# Give the daemon a moment on a fresh install.
	i=0
	while ! docker info >/dev/null 2>&1; do
		i=$((i + 1))
		if [ "$i" -gt 30 ]; then
			die "Docker is installed but not running; try: systemctl start docker"
		fi
		sleep 1
	done
}

ensure_compose() {
	if docker compose version >/dev/null 2>&1; then
		compose="docker compose"
		return 0
	fi
	if have docker-compose && docker-compose version >/dev/null 2>&1; then
		compose="docker-compose"
		return 0
	fi

	say "Docker Compose is missing. Installing the Compose plugin..."
	if have apt-get; then
		pkg_install docker-compose-plugin
	elif have dnf; then
		pkg_install docker-compose-plugin
	elif have yum; then
		pkg_install docker-compose-plugin
	fi

	if docker compose version >/dev/null 2>&1; then
		compose="docker compose"
		return 0
	fi
	die "could not install Docker Compose; see https://docs.docker.com/compose/install/"
}

if ! have docker; then
	install_docker
fi
start_docker
ensure_compose
say "Docker is ready."

# ---------------------------------------------------------------------------
# Firewall: only if one is already active, so we do not invent a policy
# ---------------------------------------------------------------------------

open_port() {
	if have ufw && ufw status 2>/dev/null | grep -qi 'Status: active'; then
		say "Opening TCP port ${PORT} in ufw..."
		ufw allow "${PORT}/tcp" comment 'OpenBackup' >/dev/null 2>&1 || ufw allow "${PORT}/tcp" || true
	elif have firewall-cmd && firewall-cmd --state 2>/dev/null | grep -qi running; then
		say "Opening TCP port ${PORT} in firewalld..."
		firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null 2>&1 || true
		firewall-cmd --reload >/dev/null 2>&1 || true
	fi
}
open_port

# ---------------------------------------------------------------------------
# Generate what the user should not have to invent
# ---------------------------------------------------------------------------

random() {
	if have openssl; then
		openssl rand -base64 24 | tr -d '/+=' | cut -c1-24
	else
		tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24
	fi
}

mkdir -p "$DIR"
cd "$DIR"

if [ -f .env ]; then
	say "Found an existing install in $DIR; keeping its settings."
	# shellcheck disable=SC1091
	. ./.env
	admin_email="${OPENBACKUP_ADMIN_EMAIL:-}"
	admin_password=""
else
	admin_email="${ADMIN_EMAIL:-admin@localhost}"
	admin_password=$(random)
	{
		echo "OPENBACKUP_PUBLIC_URL=${PUBLIC_URL}"
		echo "OPENBACKUP_ADMIN_EMAIL=${admin_email}"
		echo "OPENBACKUP_ADMIN_PASSWORD=${admin_password}"
		echo "OPENBACKUP_TRUST_PROXY=true"
		echo "OPENBACKUP_RETENTION_DAYS=30"
	} > .env
	chmod 600 .env
fi

# ---------------------------------------------------------------------------
# Image: pull first, build from git if the published image is missing
# ---------------------------------------------------------------------------

use_build=0
say "Looking for ${IMAGE}..."
if docker image inspect "$IMAGE" >/dev/null 2>&1; then
	say "Using the image already on this machine."
elif docker pull "$IMAGE" >/dev/null 2>&1; then
	say "Pulled ${IMAGE}."
else
	use_build=1
	say "Published image not found — building from ${REPO} (${REF})."
	ensure_cmd git git
	if [ -d src/.git ]; then
		say "Updating the local checkout..."
		git -C src fetch --depth 1 origin "$REF"
		git -C src checkout -q FETCH_HEAD
	else
		rm -rf src
		git clone --depth 1 --branch "$REF" "$REPO" src 2>/dev/null || {
			git clone --depth 1 "$REPO" src
			git -C src checkout -q "$REF" 2>/dev/null || true
		}
	fi
	IMAGE="foisalislambd/openbackup:local"
fi

# ---------------------------------------------------------------------------
# Compose file
# ---------------------------------------------------------------------------

# Written fresh each run so an upgrade picks up changes here, while .env (which
# holds the generated password) is only written once.
if [ "$use_build" = 1 ]; then
	cat > docker-compose.yml <<YAML
services:
  openbackup:
    image: ${IMAGE}
    build:
      context: ./src
      dockerfile: Dockerfile
    container_name: openbackup
    restart: unless-stopped
    ports:
      - '${PORT}:18200'
    volumes:
      - openbackup-data:/data
    env_file:
      - .env
    security_opt:
      - no-new-privileges:true
    read_only: true
    tmpfs:
      - /tmp

volumes:
  openbackup-data:
YAML
	say "Building the server image (first time can take a few minutes)..."
	$compose build
else
	cat > docker-compose.yml <<YAML
services:
  openbackup:
    image: ${IMAGE}
    container_name: openbackup
    restart: unless-stopped
    ports:
      - '${PORT}:18200'
    volumes:
      - openbackup-data:/data
    env_file:
      - .env
    security_opt:
      - no-new-privileges:true
    read_only: true
    tmpfs:
      - /tmp

volumes:
  openbackup-data:
YAML
fi

say "Starting OpenBackup..."
$compose up -d

# ---------------------------------------------------------------------------
# Wait until it actually answers, so failures surface here and not later
# ---------------------------------------------------------------------------

say "Waiting for the server to become ready..."
i=0
until curl -fsS "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 60 ]; then
		say ""
		say "The server did not become healthy. Recent logs:"
		$compose logs --tail 40
		die "startup failed"
	fi
	sleep 2
done

# Prefer a public IPv4; fall back to the first address hostname -I returns.
detect_ip() {
	if have curl; then
		ip=$(curl -4 -fsS --max-time 3 https://ifconfig.me 2>/dev/null || true)
		if [ -n "$ip" ]; then
			printf '%s' "$ip"
			return 0
		fi
	fi
	hostname -I 2>/dev/null | awk '{print $1}'
}

url="${PUBLIC_URL:-http://$(detect_ip):${PORT}}"

say ""
say "============================================"
say "  OpenBackup is running"
say "============================================"
say ""
say "  Open this in your browser:"
say "    ${url}"
say ""
if [ -n "$admin_password" ]; then
	say "  Login:"
	say "    Email:     ${admin_email}"
	say "    Password:  ${admin_password}"
	say ""
	say "  (saved in ${DIR}/.env — change it after you sign in)"
	say ""
fi
say "  Next steps:"
say "    1. Sign in to the dashboard"
say "    2. Go to Devices → create a connection code"
say "    3. On each Linux/macOS computer run:"
say ""
say "         curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh"
say "         openbackup connect --server ${url} --code YOUR-CODE"
say ""
say "       (Windows: download the installer from GitHub Releases)"
say ""
if [ -z "$PUBLIC_URL" ]; then
	say "  Tip: for use over the internet, point a domain at this VPS, put HTTPS"
	say "  in front (Caddy/nginx), set OPENBACKUP_PUBLIC_URL in ${DIR}/.env, then:"
	say "    cd ${DIR} && ${compose} up -d"
	say ""
fi
if [ "$use_build" = 1 ]; then
	say "  Note: built from source because no published image was on Docker Hub yet."
	say "  After you push foisalislambd/openbackup:latest, re-run this script to pull."
	say ""
fi
say "  Manage later:"
say "    cd ${DIR}"
say "    ${compose} logs -f      # watch logs"
say "    ${compose} restart      # restart"
say "    ${compose} pull && ${compose} up -d   # upgrade"
say "    ${compose} down         # stop (keeps your backups)"
say ""
