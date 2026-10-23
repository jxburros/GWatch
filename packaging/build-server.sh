#!/bin/sh
# Builds packaged GWatch server binaries.
#
#   packaging/build-server.sh <packager> <version> <outdir> <goos/goarch>...
#   packaging/build-server.sh deb 0.8.0 dist/packaged-server linux/amd64 linux/arm64 linux/arm
#
# writes <outdir>/<packager>/<goos>-<goarch>/gwatch.
#
# A packaged binary is the release binary with one more linker flag,
# -X main.packagedBy=<packager>, which makes it leave updating to the package
# manager that installed it: Settings > Updates reports new releases and says
# which command installs them, but never replaces the executable
# (internal/api/packaging.go), and `gwatch install`/`uninstall` defer to the
# package's own unit (packaging.go). It is a different binary from the
# self-updating gwatch-<os>-<arch> release assets, so it is never published
# under those names: it only ever goes into a package, and is called plain
# gwatch inside a directory of its own.
#
# Everything else matches the release job in .github/workflows/release.yml:
# CGO_ENABLED=0, -trimpath, -s -w, GOARM=7 for linux/arm. The agent's
# equivalent is packaging/build-agent.sh.
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
deb | rpm) ;;
*)
	echo "unknown packager '$packager' (deb or rpm)" >&2
	exit 2
	;;
esac

for target in "$@"; do
	goos=${target%/*}
	goarch=${target#*/}
	dir="$outdir/$packager/$goos-$goarch"
	mkdir -p "$dir"
	echo "  $packager $goos/$goarch -> $dir/gwatch"
	CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch GOARM=7 go build -trimpath \
		-ldflags "-s -w -X main.version=$version -X main.packagedBy=$packager" \
		-o "$dir/gwatch" .
done
