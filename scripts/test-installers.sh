#!/bin/sh
# Offline integration: authentic downloads execute; a forged binary with an
# internally consistent checksum never executes. Uses a fresh test key only.
set -eu
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' 0 HUP INT TERM
export fixture
mkdir "$fixture/bin"
printf '#!/bin/sh\nexit 0\n' > "$fixture/bin/jq"
printf '#!/bin/sh\ncase $1 in -s) echo Linux ;; *) echo x86_64 ;; esac\n' > "$fixture/bin/uname"
printf '#!/bin/sh\necho 0\n' > "$fixture/bin/id"
cat > "$fixture/bin/curl" <<'EOF'
#!/bin/sh
set -eu
source_file="$fixture/binary"
while [ "$#" -gt 0 ]; do
 case "$1" in
 -o) out=$2; shift 2 ;;
 *.sha256) source_file="$fixture/binary.sha256"; shift ;;
 *.sig) source_file="$fixture/binary.sig"; shift ;;
 *) shift ;;
 esac
done
cp "$source_file" "$out"
EOF
chmod +x "$fixture/bin/id" "$fixture/bin/curl" "$fixture/bin/jq" "$fixture/bin/uname"
openssl genpkey -algorithm ED25519 -out "$fixture/private.pem"
openssl pkey -in "$fixture/private.pem" -pubout -outform DER -out "$fixture/public.der"
key=$(tail -c 32 "$fixture/public.der" | openssl base64 -A)
printf '#!/bin/sh\nprintf verified > "$fixture/executed"\n' > "$fixture/binary"
openssl dgst -sha256 -binary "$fixture/binary" > "$fixture/digest"
openssl pkeyutl -sign -inkey "$fixture/private.pem" -rawin -in "$fixture/digest" -out "$fixture/signature"
printf 'gwatch-sig-v1\nkey: test\nsig: ' > "$fixture/binary.sig"
openssl base64 -A -in "$fixture/signature" >> "$fixture/binary.sig"
printf '\n' >> "$fixture/binary.sig"
openssl dgst -sha256 "$fixture/binary" | awk '{print $NF}' > "$fixture/binary.sha256"
cp "$fixture/binary" "$fixture/original"
for script in scripts/install.sh scripts/install-agent.sh; do
 cp "$fixture/original" "$fixture/binary"
 openssl dgst -sha256 "$fixture/binary" | awk '{print $NF}' > "$fixture/binary.sha256"
 sed "s|OzN/mY/5zRszwN7DxuhrC718w+3TjL36FuZHbYV/qkk=|$key|" "$script" > "$fixture/install.sh"
 PATH="$fixture/bin:$PATH" sh "$fixture/install.sh" --version 1.0.0 --server https://example.invalid --code TEST-CODE --no-start
 test -f "$fixture/executed"
 rm "$fixture/executed"
 printf '\n# tampered\n' >> "$fixture/binary"
 openssl dgst -sha256 "$fixture/binary" | awk '{print $NF}' > "$fixture/binary.sha256"
 if PATH="$fixture/bin:$PATH" sh "$fixture/install.sh" --version 1.0.0 --server https://example.invalid --code TEST-CODE --no-start; then
  echo 'forged release unexpectedly accepted' >&2
  exit 1
 fi
 test ! -f "$fixture/executed"
done
