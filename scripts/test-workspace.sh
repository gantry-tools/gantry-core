#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cortex_dir=${CORTEX_DIR:-"$repo_dir/../crtx-dev/cortex"}
warden_dir=${WARDEN_DIR:-"$repo_dir/../warden-cv/warden"}
trestle_dir=${TRESTLE_DIR:-"$repo_dir/../trestle-cv/trestle"}
watchpost_dir=${WATCHPOST_DIR:-"$repo_dir/../watchpost-cv/watchpost"}
watchpost_agent_dir=${WATCHPOST_AGENT_DIR:-"$repo_dir/../watchpost-cv/watchpost-agent"}
webfleet_dir=${WEBFLEET_DIR:-"$repo_dir/../webfleet-cv/webfleet"}

for consumer_dir in "$cortex_dir" "$warden_dir" "$trestle_dir" "$watchpost_dir" "$watchpost_agent_dir" "$webfleet_dir"; do
	test -f "$consumer_dir/go.mod"
done

cortex_dir=$(CDPATH= cd -- "$cortex_dir" && pwd)
warden_dir=$(CDPATH= cd -- "$warden_dir" && pwd)
trestle_dir=$(CDPATH= cd -- "$trestle_dir" && pwd)
watchpost_dir=$(CDPATH= cd -- "$watchpost_dir" && pwd)
watchpost_agent_dir=$(CDPATH= cd -- "$watchpost_agent_dir" && pwd)
webfleet_dir=$(CDPATH= cd -- "$webfleet_dir" && pwd)

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
cat >"$work_dir/go.work" <<EOF
go 1.25.0

use (
	$repo_dir
	$cortex_dir
	$warden_dir
	$trestle_dir
	$watchpost_dir
	$watchpost_agent_dir
	$webfleet_dir
)
EOF

export GOWORK="$work_dir/go.work"

# A consumer can pin a local, unpublished Gantry Core commit. Workspace main
# modules normally supersede required versions, but the module graph still
# resolves those pins before a first push. Add version-specific replacements so
# local integration tests remain independent of remote repository state.
for version in $(awk '
	$1 == "require" && $2 == "github.com/gantry-tools/gantry-core" { print $3 }
	$1 == "github.com/gantry-tools/gantry-core" { print $2 }
' "$cortex_dir/go.mod" "$warden_dir/go.mod" "$trestle_dir/go.mod" \
	"$watchpost_dir/go.mod" "$watchpost_agent_dir/go.mod" "$webfleet_dir/go.mod" | sort -u); do
	go work edit -replace="github.com/gantry-tools/gantry-core@$version=$repo_dir"
done

(cd "$repo_dir" && go test -race ./... && go vet ./...)

status=0
for consumer_dir in "$cortex_dir" "$warden_dir" "$trestle_dir" "$watchpost_dir" "$watchpost_agent_dir" "$webfleet_dir"; do
	(cd "$consumer_dir" && go test ./... && go vet ./...) || status=$?
done
exit "$status"
