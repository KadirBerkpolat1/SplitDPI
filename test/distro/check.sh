#!/bin/sh
set -eu

hint=$1
bin=${SPLITDPI_BIN:-/usr/local/bin/splitdpi}

if [ ! -x "$bin" ]; then
	echo "missing executable: $bin" >&2
	exit 1
fi

set +e
out=$(env PATH=/does-not-exist "$bin" doctor 2>&1)
code=$?
set -e

if [ "$code" -ne 1 ]; then
	echo "splitdpi doctor: want exit 1, got $code" >&2
	echo "$out" >&2
	exit 1
fi

echo "$out" | grep -Fq "$hint" || {
	echo "doctor output missing package hint: $hint" >&2
	echo "$out" >&2
	exit 1
}

sh -n internal/initsys/testdata/splitdpi.openrc.golden
sh -n internal/initsys/testdata/runit-run.golden
sh -n internal/initsys/testdata/runit-finish.golden

if [ -f /etc/os-release ] && grep -q '^ID=debian' /etc/os-release; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get update
	apt-get install -y systemd
	if [ "$bin" != /usr/local/bin/splitdpi ]; then
		install -m 755 "$bin" /usr/local/bin/splitdpi
	fi
	unit=/tmp/splitdpi.service
	cp internal/initsys/testdata/splitdpi.service.golden "$unit"
	systemd-analyze verify "$unit"
fi
