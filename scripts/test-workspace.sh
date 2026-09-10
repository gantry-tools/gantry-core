#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cortex_dir=${CORTEX_DIR:-"$repo_dir/../crtx-dev/cortex"}
warden_dir=${WARDEN_DIR:-"$repo_dir/../warden-cv/warden"}

test -f "$cortex_dir/go.mod"
test -f "$warden_dir/go.mod"

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
cat >"$work_dir/go.work" <<EOF
go 1.25.0

use (
	$repo_dir
	$cortex_dir
	$warden_dir
)
EOF

export GOWORK="$work_dir/go.work"

(cd "$repo_dir" && go test -race ./... && go vet ./...)

(cd "$cortex_dir" && go test ./... && go vet ./...) &
cortex_pid=$!
(cd "$warden_dir" && go test ./... && go vet ./...) &
warden_pid=$!

status=0
wait "$cortex_pid" || status=$?
wait "$warden_pid" || status=$?
exit "$status"
