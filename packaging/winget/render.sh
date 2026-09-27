#!/bin/sh
# Renders the winget manifests for one agent release.
#
#   packaging/winget/render.sh <version> <installer.exe> <outdir>
#
# writes <outdir>/GWatch.Agent.yaml, GWatch.Agent.installer.yaml and
# GWatch.Agent.locale.en-US.yaml — the multi-file layout winget-pkgs expects
# under manifests/g/GWatch/Agent/<version>/, and what `wingetcreate submit`
# takes as a directory.
#
# The installer's sha256 is computed here from the file that is about to be
# uploaded; winget refuses an installer whose hash differs, which is this
# package's integrity check. (The setup program is not Authenticode-signed —
# see docs/RELEASING.md#the-installer-artefacts — and is not in the ed25519
# self-update chain; nothing installs it but winget or a person.)
set -eu

if [ $# -ne 3 ]; then
	echo "usage: $0 <version> <installer.exe> <outdir>" >&2
	exit 2
fi
version=$1
installer=$2
outdir=$3
here=$(dirname "$0")

sha=$(sha256sum "$installer" | cut -d' ' -f1 | tr 'a-f' 'A-F')
date=$(date -u +%Y-%m-%d)
mkdir -p "$outdir"

for name in GWatch.Agent GWatch.Agent.installer GWatch.Agent.locale.en-US; do
	sed -e "s/@VERSION@/$version/g" -e "s/@SHA256@/$sha/g" -e "s/@RELEASE_DATE@/$date/g" \
		"$here/$name.yaml.tmpl" >"$outdir/$name.yaml"
done

if grep -n '@[A-Z0-9_]*@' "$outdir"/*.yaml; then
	echo "unrendered placeholders above" >&2
	exit 1
fi
ls -l "$outdir"
