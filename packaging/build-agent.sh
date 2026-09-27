#!/bin/sh
# Builds packaged gwatch-agent binaries.
#
#   packaging/build-agent.sh <packager> <version> <outdir> <goos/goarch>...
#   packaging/build-agent.sh deb 0.5.0 dist/packaged linux/amd64 linux/arm64 linux/arm
#
# writes <outdir>/<packager>/<goos>-<goarch>/gwatch-agent[.exe].
#
# A packaged binary is the release binary with one more linker flag,
# -X main.packagedBy=<packager>, which makes it leave updating to the package
# manager that installed it (cmd/gwatch-agent/packaging.go,
# docs/AGENT-DECISIONS.md entry 10). It is a different binary from the
# self-updating gwatch-agent-<os>-<arch> release assets, so it is never
# published under those names: it goes into a package (.deb, .rpm, the
# Homebrew tarball, the winget setup program, the container image), and the
# file is called plain gwatch-agent inside a directory of its own, so there is
# no path by which it could be uploaded as something the self-updater would
# pick up.
#
# Everything else matches the agent-release job in .github/workflows/ci.yml:
# CGO_ENABLED=0, -trimpath, -s -w, the default GOARM (7) for linux/arm. This
# script is the one place the packaged ldflags are written down; CI and
# `make agent-packages` both call it.
set -eu

if [ $# -lt 4 ]; then
	echo "usage: $0 <packager> <version> <outdir> <goos/goarch>..." >&2
	exit 2
fi
packager=$1
version=$2
outdir=$3
shift 3

case "$packager" in
deb | rpm | homebrew | winget | docker) ;;
*)
	echo "unknown packager '$packager' (deb, rpm, homebrew, winget or docker)" >&2
	exit 2
	;;
esac

for target in "$@"; do
	goos=${target%/*}
	goarch=${target#*/}
	ext=
	[ "$goos" = windows ] && ext=.exe
	dir="$outdir/$packager/$goos-$goarch"
	mkdir -p "$dir"
	echo "  $packager $goos/$goarch -> $dir/gwatch-agent$ext"
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
		-ldflags "-s -w -X main.version=$version -X main.packagedBy=$packager" \
		-o "$dir/gwatch-agent$ext" ./cmd/gwatch-agent
done
