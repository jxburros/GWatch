#!/bin/sh
# Authenticate with the pinned release key BEFORE executing downloaded code.
set -eu
umask 077
version=
# Empty unless --listen is given: on Linux, re-running the installer to update
# keeps the address an earlier install chose (gwatch install reads it back from
# /etc/gwatch/gwatch.env).
listen=
no_start=false
uninstall=false
while [ "$#" -gt 0 ]; do
 case "$1" in
 --version) version=$2; shift 2 ;;
 --listen) listen=$2; shift 2 ;;
 --no-start) no_start=true; shift ;;
 --uninstall) uninstall=true; shift ;;
 *) echo "unknown option: $1" >&2; exit 2 ;;
 esac
done
[ "$(id -u)" = 0 ] || { echo "Run this installer with sudo/root." >&2; exit 1; }
product=gwatch
prefix=v
if "$uninstall"; then
 # /usr/local/bin is where gwatch install has put the program on Linux since
 # #125; libexec is where earlier releases, and macOS, keep it.
 if [ -x "/usr/local/bin/$product" ] && [ "$(uname -s)" = Linux ]; then
  "/usr/local/bin/$product" uninstall
 else
  "/usr/local/libexec/$product/$product" uninstall
 fi
 echo "Service removed; data and executable retained."
 exit 0
fi
for tool in curl openssl jq; do command -v "$tool" >/dev/null || { echo "$tool is required (OpenSSL 3 for Ed25519)." >&2; exit 1; }; done
case $(uname -s) in Linux) os=linux ;; Darwin) os=darwin ;; *) echo 'Unsupported operating system' >&2; exit 1 ;; esac
case $(uname -m) in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; armv7l) arch=arm ;; *) echo 'Unsupported architecture' >&2; exit 1 ;; esac
if [ -z "$version" ]; then
 version=$(curl --proto '=https' --tlsv1.2 -fsSL 'https://api.github.com/repos/jxburros/GWatch/releases?per_page=100' | jq -er --arg prefix "$prefix" '[.[] | select(.draft == false and .prerelease == false and (.tag_name | startswith($prefix)))][0].tag_name')
else
 version="$prefix${version#"$prefix"}"
fi
case "$version" in *[!a-zA-Z0-9.-]*) echo 'Invalid version' >&2; exit 2 ;; esac
asset="$product-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
url="https://github.com/jxburros/GWatch/releases/download/$version/$asset"
for suffix in '' .sha256 .sig; do curl --proto '=https' --tlsv1.2 -fsSL "$url$suffix" -o "$tmp/binary$suffix"; done
expected=$(awk 'NR==1 {print $1}' "$tmp/binary.sha256")
actual=$(openssl dgst -sha256 "$tmp/binary" | awk '{print $NF}')
[ "$expected" = "$actual" ] || { echo 'Checksum mismatch' >&2; exit 1; }
[ "$(head -n 1 "$tmp/binary.sig")" = gwatch-sig-v1 ] || { echo 'Unknown signature format' >&2; exit 1; }
# release-sig binds the asset name and the version as well as the digest, so
# a genuinely signed binary from another release or product is refused.
sed -n 's/^release-sig: //p' "$tmp/binary.sig" | openssl base64 -d -A > "$tmp/signature"
[ -s "$tmp/signature" ] || { echo 'The signature does not name its release' >&2; exit 1; }
printf 'gwatch-release-v1\nasset: %s\nversion: %s\nsha256: %s\n' "$asset" "${version#"$prefix"}" "$actual" > "$tmp/message"
# ASN.1 SubjectPublicKeyInfo for the pinned Ed25519 key in release_keys.txt.
printf '\060\052\060\005\006\003\053\145\160\003\041\000' > "$tmp/key.der"
printf '%s' 'OzN/mY/5zRszwN7DxuhrC718w+3TjL36FuZHbYV/qkk=' | openssl base64 -d -A >> "$tmp/key.der"
openssl pkeyutl -verify -pubin -keyform DER -inkey "$tmp/key.der" -rawin -in "$tmp/message" -sigfile "$tmp/signature" >/dev/null || { echo 'Release signature verification failed' >&2; exit 1; }
chmod 700 "$tmp/binary"
if [ "$os" = linux ]; then
 # gwatch install defaults to /var/lib/gwatch and keeps whatever data
 # directory and address an earlier install wrote to /etc/gwatch/gwatch.env.
 set -- install
 if [ -n "$listen" ]; then set -- "$@" --listen "$listen"; fi
else
 set -- install --data-dir /var/lib/gwatch --listen "${listen:-127.0.0.1:7230}"
fi
if "$no_start"; then set -- "$@" --no-start; fi
"$tmp/binary" "$@"
echo "GWatch installed. Open http://${listen:-127.0.0.1:7230} (or use an SSH tunnel)."
echo 'First administrator: ssh -L 7230:127.0.0.1:7230 user@server, then open http://127.0.0.1:7230 and Settings > Users & access.'
