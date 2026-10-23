#!/bin/sh
# Builds the GWatch server .deb and .rpm for every Linux architecture GWatch
# is released for.
#
#   packaging/nfpm/gwatch/package.sh <version> <outdir> [amd64 arm64 arm]
#
# Run from the repository root. Each package gets its own packaged binary
# (packaging/build-server.sh, -X main.packagedBy=deb or rpm), so the .deb says
# "apt" and the .rpm says "dnf" when asked to update. nfpm is pinned; set NFPM
# to use a copy already installed:
#
#   NFPM=nfpm packaging/nfpm/gwatch/package.sh 0.8.0 dist/server-packages
#
# FORMATS="deb" builds only the .deb (CI's upgrade test needs no .rpm).
#
# Output file names are nfpm's own, gwatch_<v>-1_<arch>.deb and
# gwatch-<v>-1.<arch>.rpm, neither of which the self-updater's exact
# gwatch-<os>-<arch> match can ever mistake for an update. The agent's
# packages are built the same way by packaging/nfpm/package.sh.
set -eu

if [ $# -lt 2 ]; then
	echo "usage: $0 <version> <outdir> [arch...]" >&2
	exit 2
fi
version=$1
outdir=$2
shift 2
[ $# -gt 0 ] || set -- amd64 arm64 arm

NFPM=${NFPM:-go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0}
bindir=${PACKAGED_BIN_DIR:-dist/packaged-server}
formats=${FORMATS:-deb rpm}
mkdir -p "$outdir"

for arch in "$@"; do
	case "$arch" in
	amd64 | arm64) nfpm_arch=$arch ;;
	# GOARM=7, what the release job builds gwatch-linux-arm with: armhf.
	arm) nfpm_arch=arm7 ;;
	*)
		echo "unsupported architecture '$arch'" >&2
		exit 2
		;;
	esac
	for format in $formats; do
		sh packaging/build-server.sh "$format" "$version" "$bindir" "linux/$arch"
		# gwatch.yaml names this one fixed path (nfpm does not expand
		# variables in a source path), so the binary for this package is put
		# there first.
		mkdir -p dist/nfpm-server
		cp "$bindir/$format/linux-$arch/gwatch" dist/nfpm-server/gwatch
		GWATCH_VERSION=$version NFPM_ARCH=$nfpm_arch \
			$NFPM package --config packaging/nfpm/gwatch.yaml --packager "$format" --target "$outdir/"
	done
	rm -f dist/nfpm-server/gwatch
done
ls -l "$outdir"
