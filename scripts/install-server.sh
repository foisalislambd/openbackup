#!/bin/sh
# OpenBackup server installer for a VPS.
#
#   curl -fsSL https://raw.githubusercontent.com/openbackup/openbackup/main/scripts/install-server.sh | sh
#
# It writes a docker compose file to /opt/openbackup, generates an admin password,
# starts the server, and prints how to reach it. Nothing else on the machine is
# touched; removing the directory and the Docker volume removes everything.
set -eu

DIR="${OPENBACKUP_DIR:-/opt/openbackup}"
IMAGE="${OPENBACKUP_IMAGE:-openbackup/server:latest}"
PORT="${OPENBACKUP_PORT:-8080}"
PUBLIC_URL="${OPENBACKUP_PUBLIC_URL:-}"
ADMIN_EMAIL="${OPENBACKUP_ADMIN_EMAIL:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

command -v docker >/dev/null 2>&1 || die "Docker is required. Install it with: curl -fsSL https://get.docker.com | sh"

if docker compose version >/dev/null 2>&1; then
	compose="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
	compose="docker-compose"
else
	die "the Docker Compose plugin is required (docker compose version)"
fi

if [ "$(id -u)" != "0" ] && ! docker info >/dev/null 2>&1; then
	die "this needs root, or a user in the docker group; try again with sudo"
fi

# ---------------------------------------------------------------------------
# Generate what the user should not have to invent
# ---------------------------------------------------------------------------

random() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -base64 24 | tr -d '/+=' | cut -c1-24
	else
		# /dev/urandom is always there; tr keeps it to characters that survive a
		# copy and paste out of a terminal.
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
# Compose file
# ---------------------------------------------------------------------------

# Written fresh each run so an upgrade picks up changes here, while .env (which
# holds the generated password) is only written once.
cat > docker-compose.yml <<YAML
services:
  openbackup:
    image: ${IMAGE}
    container_name: openbackup
    restart: unless-stopped
    ports:
      - '${PORT}:8080'
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

say "Pulling the server image..."
$compose pull --quiet 2>/dev/null || $compose pull

say "Starting..."
$compose up -d

# ---------------------------------------------------------------------------
# Wait until it actually answers, so failures surface here and not later
# ---------------------------------------------------------------------------

i=0
until curl -fsS "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 30 ]; then
		say ""
		say "The server did not become healthy. Recent logs:"
		$compose logs --tail 30
		die "startup failed"
	fi
	sleep 1
done

url="${PUBLIC_URL:-http://$(hostname -I 2>/dev/null | awk '{print $1}'):${PORT}}"

say ""
say "OpenBackup is running."
say ""
say "  Dashboard:  ${url}"
if [ -n "$admin_password" ]; then
	say "  Email:      ${admin_email}"
	say "  Password:   ${admin_password}"
	say ""
	say "That password is stored in ${DIR}/.env. Change it after signing in."
fi
say ""
say "Next: open the dashboard, go to Devices, create a connection code, then on the"
say "computer you want to back up run:"
say ""
say "  curl -fsSL ${url}/install.sh | sh"
say "  openbackup connect --server ${url} --code YOUR-CODE"
say ""
if [ -z "$PUBLIC_URL" ]; then
	say "Before using this over the internet, put it behind TLS and set"
	say "OPENBACKUP_PUBLIC_URL in ${DIR}/.env to the https address, then run"
	say "'$compose up -d' again. Device tokens travel in headers and must not"
	say "cross the internet unencrypted."
fi
say "Manage it with: cd ${DIR} && ${compose} [logs|restart|pull|down]"
