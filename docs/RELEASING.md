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

The version lives in the [`VERSION`](../VERSION) file at the repository root.
That is what a local `make build`, `scripts\build.ps1` and both setup programs
read, so a build always reports the version of the commit it came from. A
release is that file plus a matching tag:

```sh
# 1. Set the version and write the changelog entry
echo 0.2.0 > VERSION
$EDITOR CHANGELOG.md
git commit -am "Release 0.2.0"
git push

# 2. Tag it
git tag v0.2.0
git push origin v0.2.0
```

The tag must match `VERSION` exactly, `v` prefix aside. CI checks this first and
refuses the release otherwise, because a mismatch ships binaries whose own
`gwatch version` output contradicts the file they were downloaded from.

GWatch is pre-1.0 while it is in beta. A `0.x` version switches on the beta
notices in the web interface and on the installers' welcome page; they turn
themselves off at `1.0.0`, with nothing to remember to remove.

The `release` job in `.github/workflows/ci.yml` then:

1. fails immediately if the tag and `VERSION` disagree;
2. fails immediately if `GWATCH_SIGNING_KEY` is unset, or if its public key is
   not listed in `release_keys.txt` — that combination would publish binaries
   that reject their own updates;
3. cross-compiles `gwatch-<os>-<arch>[.exe]` for Windows, Linux and macOS and
   writes a `.sha256` next to each;
4. cross-compiles the MCP companion (`gwatch-mcp-<os>-<arch>`, versioned from
   `mcp/VERSION`) and the hardware agent (`gwatch-agent-<os>-<arch>`, which adds
   `linux/arm` for Raspberry Pi class machines). Both names sit inside the
   `dist/gwatch-*` glob so they are signed and checksummed like everything else,
   and both stay invisible to the in-app updater, which matches only the exact
   names `gwatch-<os>-<arch>[.exe]`;
5. signs every binary (`gwatch-sign sign`) and verifies the result against the
   pinned keys (`gwatch-sign verify`) — the same check the updater runs on the
   user's machine;
6. publishes all of `dist/*`, binaries plus `.sha256` plus `.sig`, as the
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

## The installer artefacts

The `release` job also compiles both Inno Setup scripts and publishes the results
alongside the raw binaries:

| Script | Built from | Published as |
|---|---|---|
| `scripts/installer/gwatch.iss` | `dist/gwatch-windows-amd64.exe` | `gwatch-setup-<version>.exe` |
| `scripts/installer/gwatch-agent.iss` | `dist/gwatch-agent-windows-amd64.exe` | `gwatch-agent-setup-<version>.exe` |

Both are compiled with `iscc /DAppVersion=<tag> /DExePath=<exe>` — see
[`scripts/installer/README.md`](../scripts/installer/README.md) for what they do and how
to build them locally. Three things to keep in mind:

- Neither is **Authenticode-signed**. That's a real follow-up (Windows SmartScreen shows
  an "unknown publisher" warning until it is), separate from the ed25519 release
  signing described above — signing an `.exe` with a code-signing certificate is a
  different mechanism with its own cost/process, tracked but not yet done.
- Neither is picked up by the in-app self-updater. `pickAsset` (in
  `internal/update`) only matches `gwatch-<os>-<arch>[.exe]`, so `*-setup-*.exe`
  is invisible to it by design — an installer is a first-install/reinstall path, not an
  update payload. The updater keeps managing the plain `gwatch.exe` binary in place as
  it does today.
- They are built **after** the signing step, so they carry no `.sig` or `.sha256` of
  their own. That is deliberate — they are outside the signed self-update chain — but it
  means the release notes should point people at the raw binaries if they want something
  they can verify.

The wizard artwork under `scripts/installer/assets/` is committed, not generated at
release time, so a release needs no image tooling on the runner. If the mark ever
changes, `web/logo.svg` and those assets have to be updated together.

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
