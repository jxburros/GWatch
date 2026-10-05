#!/bin/sh
# Renders the Homebrew formula from its template.
#
#   packaging/homebrew/render.sh <version> <dir-with-tarballs> > gwatch-agent.rb
#
# Each tarball's sha256 is computed here, from the file that is about to be
# uploaded, so the formula pins exactly the bytes in the release: Homebrew
# refuses a download whose hash does not match, which is this package's
# integrity check (the tarballs are not in the ed25519-signed self-update
# chain, and do not need to be — nothing installs them but Homebrew).
set -eu

if [ $# -ne 2 ]; then
	echo "usage: $0 <version> <dir>" >&2
	exit 2
fi
version=$1
dir=$2
here=$(dirname "$0")

sum() {
	f="$dir/gwatch-agent-homebrew-$version-$1.tar.gz"
	if [ ! -f "$f" ]; then
		echo "missing $f" >&2
		exit 1
	fi
	sha256sum "$f" | cut -d' ' -f1
}

darwin_arm64=$(sum darwin-arm64) || exit 1
darwin_amd64=$(sum darwin-amd64) || exit 1
linux_arm64=$(sum linux-arm64) || exit 1
linux_amd64=$(sum linux-amd64) || exit 1
sed -e "s/@VERSION@/$version/g" \
	-e "s/@SHA256_DARWIN_ARM64@/${darwin_arm64}/" \
	-e "s/@SHA256_DARWIN_AMD64@/${darwin_amd64}/" \
	-e "s/@SHA256_LINUX_ARM64@/${linux_arm64}/" \
	-e "s/@SHA256_LINUX_AMD64@/${linux_amd64}/" \
	"$here/gwatch-agent.rb.tmpl"
