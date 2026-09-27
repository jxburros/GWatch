#!/bin/sh
# Builds the tarballs the Homebrew formula installs from, and renders the
# formula for them.
#
#   packaging/homebrew/package.sh <version> <outdir>
#
# Run from the repository root. Writes
#   <outdir>/gwatch-agent-homebrew-<version>-<os>-<arch>.tar.gz   (four of them)
#   <outdir>/gwatch-agent.rb                                       (the formula)
#
# The tarballs hold a binary built with -X main.packagedBy=homebrew
# (packaging/build-agent.sh) and the licence. Their names carry "homebrew" and
# end in .tar.gz, and the self-updater matches gwatch-agent-<os>-<arch> exactly
# and never considers a .tar.gz, so a release build can never take one of
# these for an update (TestPackagedAssetsAreInvisibleToTheUpdater).
#
# macOS and Linux, amd64 and arm64: the platforms Homebrew runs on. The
# formula picks one by on_macos/on_linux and on_arm/on_intel.
set -eu

if [ $# -ne 2 ]; then
	echo "usage: $0 <version> <outdir>" >&2
	exit 2
fi
version=$1
outdir=$2
bindir=${PACKAGED_BIN_DIR:-dist/packaged}
mkdir -p "$outdir"

sh packaging/build-agent.sh homebrew "$version" "$bindir" darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

for target in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64; do
	stage="$bindir/homebrew/$target"
	cp LICENSE "$stage/LICENSE"
	# Owner, group and times are normalised so that rebuilding the same
	# version gives the same bytes, and so the same sha256 in the formula.
	tar -C "$stage" --owner=0 --group=0 --numeric-owner --mtime='2000-01-01 00:00:00Z' \
		-cf - gwatch-agent LICENSE | gzip -n -9 >"$outdir/gwatch-agent-homebrew-$version-$target.tar.gz"
done

sh packaging/homebrew/render.sh "$version" "$outdir" >"$outdir/gwatch-agent.rb"
ls -l "$outdir"
