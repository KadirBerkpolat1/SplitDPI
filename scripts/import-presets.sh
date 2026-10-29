#!/bin/sh
# Replace the embedded seed with a SplitDPI-presets release, after checking it.
# Usage: scripts/import-presets.sh TAG
set -eu

tag=${1:?usage: import-presets.sh TAG}
root=$(cd "$(dirname "$0")/.." && pwd)
data=$root/internal/presets/data
url=https://github.com/KadirBerkpolat1/SplitDPI-presets/releases/download/$tag/presets.tar.gz

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/tree"

curl -fsSL -o "$tmp/presets.tar.gz" "$url"
tar -xzf "$tmp/presets.tar.gz" -C "$tmp/tree" --no-same-owner --no-same-permissions
(cd "$root" && go run ./cmd/splitdpi presets check "$tmp/tree")

rm -rf "$data"
mkdir -p "$data"
cp -R "$tmp/tree/." "$data/"
echo "Imported presets $(cat "$data/VERSION") into $data"
