#!/bin/sh
# Print the release version from VERSION.md.
#
# The version is the first non-empty line that is not a comment (the first
# non-whitespace character is not #). Tooling adds the leading v itself.
# A single leading v or V is stripped so a hand-edited line still parses;
# CI rejects that form and requires a bare semver in the committed file.
#
# Usage: read-version.sh [path]
set -eu

file=${1:-VERSION.md}

if [ ! -f "$file" ]; then
	echo "read-version.sh: $file not found" >&2
	exit 1
fi

version=$(awk '
	{ sub(/\r$/, "") }
	/^[[:space:]]*#/ { next }
	/^[[:space:]]*$/ { next }
	{ print; exit }
' "$file")

if [ -z "$version" ]; then
	echo "read-version.sh: no version in $file" >&2
	exit 1
fi

case "$version" in
	v* | V*) version=${version#?} ;;
esac

case "$version" in
	*[[:space:]]*)
		echo "read-version.sh: version contains whitespace: $version" >&2
		exit 1
		;;
esac

printf '%s\n' "$version"
