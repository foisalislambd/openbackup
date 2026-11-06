#!/bin/sh
# Print Go -ldflags that stamp internal/version from VERSION.md.
#
# COMMIT and DATE, when set, override the git revision and the build time.
# Usage: go-ldflags.sh [repo-root]
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root=${1:-.}
if [ "$root" = "." ]; then
	root=$(CDPATH='' cd -- "$script_dir/.." && pwd)
fi

ver=$(sh "$script_dir/read-version.sh" "$root/VERSION.md")

commit=${COMMIT:-}
if [ -z "$commit" ]; then
	commit=$(git -C "$root" rev-parse --short=7 HEAD 2>/dev/null || true)
fi
if [ -z "$commit" ]; then
	commit=unknown
fi

date=${DATE:-}
if [ -z "$date" ]; then
	date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
fi

# These values are placed in a single compiler flag. Reject anything that
# could split the flag or select a different symbol.
for part in "$ver" "$commit" "$date"; do
	case "$part" in
		*[!0-9A-Za-z.+T:-]*)
			echo "go-ldflags.sh: unsafe version metadata: $part" >&2
			exit 1
			;;
	esac
done

pkg=github.com/foisalislambd/openbackup/internal/version
printf '%s\n' "-s -w -X ${pkg}.Version=v${ver} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}"
