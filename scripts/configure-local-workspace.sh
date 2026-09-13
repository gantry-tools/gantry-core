#!/bin/sh
set -eu

core_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
workspace_root=${GANTRY_WORKSPACE_ROOT:-"$(dirname -- "$core_dir")"}
target="$workspace_root/go.work"

consumers='crtx-dev/cortex
warden-cv/warden
trestle-cv/trestle
watchpost-cv/watchpost
watchpost-cv/watchpost-agent
webfleet-cv/webfleet'

test -f "$core_dir/go.mod" || {
	echo "gantry-core go.mod not found at $core_dir" >&2
	exit 1
}
for relative in $consumers; do
	test -f "$workspace_root/$relative/go.mod" || {
		echo "consumer go.mod not found at $workspace_root/$relative" >&2
		exit 1
	}
done

versions=$(
	for relative in $consumers; do
		awk '
			$1 == "require" && $2 == "github.com/gantry-tools/gantry-core" { print $3 }
			$1 == "github.com/gantry-tools/gantry-core" { print $2 }
		' "$workspace_root/$relative/go.mod"
	done | sort -u
)
test -n "$versions" || {
	echo "no gantry-core dependency found in consumer go.mod files" >&2
	exit 1
}

tmp=$(mktemp "$workspace_root/.go.work.tmp.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
{
	printf '%s\n\n' 'go 1.25.0'
	printf '%s\n' 'use ('
	printf '\t./gantry-core\n'
	for relative in $consumers; do
		printf '\t./%s\n' "$relative"
	done
	printf '%s\n' ')'
	for version in $versions; do
		printf '\nreplace github.com/gantry-tools/gantry-core %s => ./gantry-core\n' "$version"
	done
} >"$tmp"
chmod 0644 "$tmp"
mv -- "$tmp" "$target"
trap - EXIT HUP INT TERM
echo "Configured $target to use the local gantry-core checkout."
