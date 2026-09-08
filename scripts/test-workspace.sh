#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cortex_dir=${CORTEX_DIR:-"$repo_dir/../crtx-dev/cortex"}
warden_dir=${WARDEN_DIR:-"$repo_dir/../warden-cv/warden"}

test -f "$cortex_dir/go.mod"
test -f "$warden_dir/go.mod"

(cd "$repo_dir" && go test -race ./... && go vet ./...)

(cd "$cortex_dir" && go test ./... && go vet ./...) &
cortex_pid=$!
(cd "$warden_dir" && go test ./... && go vet ./...) &
warden_pid=$!

status=0
wait "$cortex_pid" || status=$?
wait "$warden_pid" || status=$?
exit "$status"
