#!/bin/sh
# Builds the gwatch-agent .deb and .rpm for every Linux architecture the agent
# is released for.
#
#   packaging/nfpm/package.sh <version> <outdir> [amd64 arm64 arm]
#
# Run from the repository root. Each package gets its own packaged binary
# (packaging/build-agent.sh, -X main.packagedBy=deb or rpm), so the .deb says
# "apt" and the .rpm says "dnf" when asked to update. nfpm is pinned; set NFPM
# to use a copy already installed:
#
#   NFPM=nfpm packaging/nfpm/package.sh 0.5.0 dist/packages
#
# Output file names are nfpm's own, gwatch-agent_<v>_<arch>.deb and
# gwatch-agent-<v>-1.<arch>.rpm, neither of which the self-updater's exact
# gwatch-agent-<os>-<arch> match can ever mistake for an update
# (TestPackagedAssetsAreInvisibleToTheUpdater).
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
bindir=${PACKAGED_BIN_DIR:-dist/packaged}
mkdir -p "$outdir"

for arch in "$@"; do
	case "$arch" in
	amd64 | arm64) nfpm_arch=$arch ;;
	# GOARM=7, the Go default and what the agent-release job builds: armhf.
	arm) nfpm_arch=arm7 ;;
	*)
		echo "unsupported architecture '$arch'" >&2
		exit 2
		;;
	esac
	for format in deb rpm; do
		sh packaging/build-agent.sh "$format" "$version" "$bindir" "linux/$arch"
		# nfpm.yaml names this one fixed path (it does not expand variables in
		# a source path), so the binary for this package is put there first.
		mkdir -p dist/nfpm
		cp "$bindir/$format/linux-$arch/gwatch-agent" dist/nfpm/gwatch-agent
		AGENT_VERSION=$version NFPM_ARCH=$nfpm_arch \
			$NFPM package --config packaging/nfpm/nfpm.yaml --packager "$format" --target "$outdir/"
	done
	rm -f dist/nfpm/gwatch-agent
done
ls -l "$outdir"
