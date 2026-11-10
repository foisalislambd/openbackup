#!/bin/sh
# Backup and restore through a running OpenBackup server container.
#
# The published image contains only the server. This script runs the agent on
# the host, which is the same split a user has. It is not a health check.
#
# Usage, with the container already up and an admin account seeded:
#   scripts/docker-roundtrip.sh ob http://127.0.0.1:18200
set -eu

container=${1:?container name}
url=${2:?server url}
agent=${OPENBACKUP_AGENT:-./bin/openbackup}
docker=${DOCKER:-docker}
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT

if [ ! -x "$agent" ]; then
  echo "building agent"
  go build -o "$agent" ./cmd/openbackup
fi

invite=$($docker exec "$container" /usr/local/bin/openbackup-server invite)
code=$(printf '%s\n' "$invite" | awk 'match($0, /[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}/) { print substr($0, RSTART, RLENGTH); exit }')
if [ -z "$code" ]; then
  printf '%s\n' "$invite" >&2
  echo "could not read an enrolment code" >&2
  exit 1
fi

src=$root/src
mkdir -p "$src/docs"
printf 'hello from docker\n' > "$src/docs/note.txt"
printf 'second\n' > "$src/docs/other.txt"
want=$(sha256sum "$src/docs/note.txt" "$src/docs/other.txt")

cfg=$root/config.json
state=$root/state
mkdir -p "$state"
OPENBACKUP_CONFIG=$cfg OPENBACKUP_STATE_DIR=$state \
  "$agent" connect --server "$url" --code "$code" --name "docker-roundtrip"

python3 - "$cfg" "$src" <<'PY'
import json, sys
path, src = sys.argv[1], sys.argv[2]
cfg = json.load(open(path))
cfg["roots_chosen"] = True
cfg["roots"] = [{"name": "box", "path": src, "enabled": True}]
limits = cfg.get("limits") or {}
limits.update({
    "max_cpu_percent": 100,
    "pause_on_metered": False,
    "pause_while_fullscreen": False,
    "pause_on_battery": False,
    "upload_bytes_per_sec": 0,
    "read_bytes_per_sec": 0,
    "priority": "normal",
    "agent_cpu_percent": 0,
    "memory_limit_bytes": -1,
})
cfg["limits"] = limits
json.dump(cfg, open(path, "w"))
PY

OPENBACKUP_CONFIG=$cfg OPENBACKUP_STATE_DIR=$state "$agent" backup
restore=$root/restore
OPENBACKUP_CONFIG=$cfg OPENBACKUP_STATE_DIR=$state \
  "$agent" restore --snapshot latest --to "$restore" --overwrite
got=$(sha256sum "$restore/box/docs/note.txt" "$restore/box/docs/other.txt" | sed "s|$restore/box/|$src/|")
if [ "$want" != "$got" ]; then
  echo "restore checksum mismatch" >&2
  echo "want: $want" >&2
  echo "got:  $got" >&2
  exit 1
fi
echo "docker backup and restore matched"
