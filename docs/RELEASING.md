# Releasing GWatch

GWatch updates itself in place, so a release is only useful if the running
binary can prove the file it just downloaded came from you. Every release asset
is signed with an ed25519 key whose public half is compiled into the binary; an
update that does not verify is refused, not installed with a warning.

## One-time setup

Do this once, on a machine you trust, before the first signed release.

1. **Generate the key pair.**

   ```sh
   make keygen            # or: go run ./cmd/gwatch-sign keygen -out release.key
   ```

   This writes `release.key` (mode 0600) and prints two things: the public key
   line and the base64 seed. `release.key` lands in the working directory, so
   generate it outside the checkout (`make keygen KEY=~/gwatch-release.key`) or
   add `release.key` to `.gitignore` — it must never be committed.

2. **Pin the public key.** Paste the printed `ed25519:<base64>` line into
   [`internal/update/release_keys.txt`](../internal/update/release_keys.txt),
   below the comment. Commit it. Every binary built from then on trusts that
   key.

3. **Store the private key as a repository secret.** In GitHub →
   *Settings → Secrets and variables → Actions → New repository secret*, name it
   `GWATCH_SIGNING_KEY` and paste the base64 seed (the last line of
   `release.key`).

4. **Keep `release.key` offline.** It is the only thing that can sign a release.
   It is not in the repository and must never be committed — back it up
   somewhere safe (a password manager or an encrypted volume). `release.key` is
   the file to guard; the GitHub secret is a copy, not the original.

## Cutting a release

```sh
git tag v1.2.0
git push origin v1.2.0
```

The `release` job in `.github/workflows/ci.yml` then:

1. fails immediately if `GWATCH_SIGNING_KEY` is unset, or if its public key is
   not listed in `release_keys.txt` — that combination would publish binaries
   that reject their own updates;
2. cross-compiles `gwatch-<os>-<arch>[.exe]` for Windows, Linux and macOS and
   writes a `.sha256` next to each;
3. cross-compiles the MCP companion (`gwatch-mcp-<os>-<arch>`, versioned from
   `mcp/VERSION`) and the hardware agent (`gwatch-agent-<os>-<arch>`, which adds
   `linux/arm` for Raspberry Pi class machines). Both names sit inside the
   `dist/gwatch-*` glob so they are signed and checksummed like everything else,
   and both stay invisible to the in-app updater, which matches only the exact
   names `gwatch-<os>-<arch>[.exe]`;
4. signs every binary (`gwatch-sign sign`) and verifies the result against the
   pinned keys (`gwatch-sign verify`) — the same check the updater runs on the
   user's machine;
5. publishes all of `dist/*`, binaries plus `.sha256` plus `.sig`, as the
   release.

To do the same by hand:

```sh
make build                     # or the cross-compile loop from ci.yml
make sign KEY=release.key      # writes dist/<asset>.sig
make verify-release            # checks them against release_keys.txt
```

## Signature format

Each asset gets a sibling `<asset>.sig`:

```
gwatch-sig-v1
key: 1a2b3c4d
sig: <base64 ed25519 signature>
```

`key` is the first 4 bytes of `sha256(public key)`, in hex — a hint for humans
and logs, not a credential. The signature covers the **SHA-256 digest** of the
asset rather than its bytes, so the signer and the updater both stream the file
and hold only 32 bytes in memory.

## Rotating the key

`release_keys.txt` may list several keys, and a release signed by any of them is
accepted. To rotate without stranding installed copies:

1. generate the new key, add its line to `release_keys.txt` alongside the old
   one, and ship a release still signed with the **old** key — installed copies
   accept it and now trust both keys;
2. switch the `GWATCH_SIGNING_KEY` secret to the new seed and release again;
3. once everyone has the transitional version, delete the old line.

If a key is compromised, drop its line at once and release with a new one. Users
on older binaries will not accept that release and must reinstall by hand — say
so in the release notes.

## The installer artefact

The `release` job also compiles `scripts/installer/gwatch.iss` with Inno Setup (via
`iscc /DAppVersion=<tag> /DExePath=dist/gwatch-windows-amd64.exe`) and publishes
`gwatch-setup-<version>.exe` alongside the raw binaries — see
[`scripts/installer/README.md`](../scripts/installer/README.md) for what it does and how
to build it locally. Two things to keep in mind:

- It is **not** Authenticode-signed. That's a real follow-up (Windows SmartScreen shows
  an "unknown publisher" warning until it is), separate from the ed25519 release
  signing described above — signing an `.exe` with a code-signing certificate is a
  different mechanism with its own cost/process, tracked but not yet done.
- It is **not** picked up by the in-app self-updater. `pickAsset` (in
  `internal/update`) only matches `gwatch-<os>-<arch>[.exe]`, so `gwatch-setup-*.exe`
  is invisible to it by design — the installer is a first-install/reinstall path, not an
  update payload. The updater keeps managing the plain `gwatch.exe` binary in place as
  it does today.

## Forks and private builds

A fork that publishes its own releases does not have to edit
`release_keys.txt`; it can pin its key at build time:

```sh
go build -ldflags "-s -w \
  -X main.version=1.2.0 \
  -X github.com/jxburros/GWatch/internal/update.releaseKeys=ed25519:AAAA..." .
```

Several keys may be given, comma-separated. A build with no key at all still
reports that a new release exists, but Settings › Updates refuses to install it
and says so.
