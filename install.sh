#!/bin/sh
set -eu

SPLITDPI_VERSION=${SPLITDPI_VERSION:-latest}
SPLITDPI_BASE_URL=${SPLITDPI_BASE_URL:-https://github.com/KadirBerkpolat1/SplitDPI/releases}
SPLITDPI_PREFIX=${SPLITDPI_PREFIX:-/usr/local}

arch=$(uname -m)
case "$arch" in
x86_64) goarch=amd64 ;;
aarch64) goarch=arm64 ;;
*)
	echo "unsupported architecture: $arch" >&2
	exit 1
	;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	checksum_cmd=sha256sum
elif command -v shasum >/dev/null 2>&1; then
	checksum_cmd=shasum
else
	echo "need sha256sum or shasum to verify downloads" >&2
	exit 1
fi

bindir=$SPLITDPI_PREFIX/bin
if [ "$(id -u)" -ne 0 ]; then
	dir=$bindir
	while [ ! -d "$dir" ]; do
		dir=$(dirname "$dir")
	done
	if [ ! -w "$dir" ]; then
		echo "rerun with sudo" >&2
		exit 1
	fi
fi
mkdir -p "$bindir"

asset=splitdpi-linux-$goarch
if [ "$SPLITDPI_VERSION" = latest ]; then
	base=$SPLITDPI_BASE_URL/latest/download
else
	base=$SPLITDPI_BASE_URL/download/$SPLITDPI_VERSION
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT INT HUP TERM

fetch() {
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
	else
		echo "need curl or wget to download" >&2
		exit 1
	fi
}

fetch "$base/$asset" "$tmpdir/$asset"
fetch "$base/sha256sums.txt" "$tmpdir/sha256sums.txt"

cd "$tmpdir"
n=$(grep -Ec " ${asset}\$" sha256sums.txt || true)
if [ "$n" -ne 1 ]; then
	echo "sha256sums.txt: want exactly one checksum line for $asset, got $n" >&2
	exit 1
fi
grep -E " ${asset}\$" sha256sums.txt >split.check
if [ "$checksum_cmd" = sha256sum ]; then
	sha256sum -c split.check
else
	shasum -a 256 -c split.check
fi

install -m 0755 "$tmpdir/$asset" "$bindir/splitdpi"
echo "sudo splitdpi"
