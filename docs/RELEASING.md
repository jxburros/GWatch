# Releasing GWatch

GWatch updates itself in place, so a release is only useful if the running
binary can prove the file it just downloaded came from you. Every release asset
is signed with an ed25519 key whose public half is compiled into the binary; an
update that does not verify is refused, not installed with a warning.

## Building and testing locally

```bash
make test                         # go vet + unit and integration tests (SQLite)
make ci                           # local checks (CI also tests platform/backend integrations): gofmt, vet, go mod tidy, race tests,
                                  #   the mcp/ module, the jsdom web suite and the Playwright/axe suite
make cover                        # race tests plus a per-function coverage report
make test-postgres / test-mysql   # the store, backup, API, engine and hostmon suites against a server
make web-check                    # node --check over every web/*.js (needs node on PATH)
make web-test                     # the jsdom suite in tests/web/
make web-e2e                      # Playwright + axe over every route, in tests/e2e/
make build / build-ncruces / build-cgo   # build with each of the three SQLite drivers
make windows                      # cross-compile dist/gwatch.exe from Linux/macOS
make docker                       # build the container image (see DOCKER.md)
make agent                        # build dist/gwatch-agent for this platform
make agent-all                    # build it for Windows, Linux and macOS, amd64/arm64/arm
make agent-packages               # the agent's .deb and .rpm, into dist/packages (nfpm, pinned, via go run)
make agent-image                  # the agent's container image, for this platform
make mcp-build / mcp-test / mcp-fmt   # the same, for the mcp/ module (see mcp/README.md)
make keygen / sign / verify-release   # the release signing key and release assets (below)
```

The test suite needs no external network: HTTP, TLS and DNS checks are exercised against
local `httptest` servers and a fake in-process DNS resolver, and ping output is parsed
from fixtures, so the tests are deterministic on a CI runner. See the [`Makefile`](../Makefile)
for what each target actually runs.

By default every test runs on SQLite. The store, backup, API, engine and hostmon suites
also run against a PostgreSQL or MySQL server when `GWATCH_TEST_DB` and a DSN are set —
see [`DATABASE.md`](DATABASE.md#for-developers); CI does this on the Linux job with
service containers.

## Continuous integration

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on every push and pull
request with the following jobs:

| Job | Runner | What it does |
|---|---|---|
| `ci` ("Lint, build, test (Windows)") | `windows-latest` | gofmt, vet, `go mod tidy`/`verify`, build + full test suite, the mcp/ module, web-asset `node --check` plus the jsdom suite (`npm test`, `tests/web/`), PowerShell script parsing, compiles both Inno Setup installers, and dry-runs the release's Authenticode signing with a throwaway certificate ([below](#authenticode-signing)) |
| `linux` ("Test (Linux)") | `ubuntu-latest` | gofmt, vet, staticcheck, build, full test and race suites (root and mcp/), the migration/store/backup/api/engine/hostmon suites against PostgreSQL 16 and MySQL 8 service containers ([`DATABASE.md`](DATABASE.md#for-developers)), `govulncheck` for both modules, and the browser accessibility suite (`npm run test:e2e` — Playwright drives Chromium through every route with an axe-core scan) |
| `macos` ("Test (macOS)") | `macos-latest` | vet + test only — deliberately lean, but this is what actually compiles and exercises `internal/sysmetrics/collect_darwin.go` |
| `docker` ("Container image") | `ubuntu-latest` | builds the `Dockerfile` for `linux/amd64` and `linux/arm/v7`, checks `gwatch version` inside it, then starts the container and waits for `/api/health` to answer 200 |
| `server-packaging` ("Server packages") | `ubuntu-latest` | builds the server's `.deb`/`.rpm` for all three architectures and checks the `.deb`'s contents, then `scripts/ci/linux-service-test.sh package`: install, the same service assertions as `linux-service` (exposure at most 2.0), refuse to self-update or register a second service, upgrade to a newer `.deb` with the data intact, remove (data kept) and purge (nothing left) — see ["The server packages"](#the-server-packages) |
| `linux-service` ("Service lifecycle (Linux)") | `ubuntu-latest` | `scripts/ci/linux-service-test.sh binary`: `gwatch install`, then asserts the unit runs as `gwatch` with `/var/lib/gwatch` and only `CAP_NET_RAW`, passes `systemd-analyze verify` and scores at most 4.5 in `systemd-analyze security`, answers `/api/health`, takes a first administrator, a sign-in and a ping check with a result, and leaves nothing behind after `gwatch uninstall --purge --yes` |
| `agent-packaging` ("Agent packages") | `ubuntu-latest` | builds the agent's `.deb`/`.rpm` for all three architectures, installs the `.deb` on the runner's systemd and takes it through install → refuse to self-update → configure → run → purge, renders the Homebrew formula (`ruby -c`) and the winget manifests (validated against winget's 1.6.0 schemas), builds the agent image and has it read the runner through a mounted host root, and checks no package output is named like a self-update asset — see ["The agent packages"](#the-agent-packages) |

Windows builds the Inno Setup installers and validates PowerShell scripts. Linux tests
the installed systemd service and journal output; Linux and macOS both exercise their
native collectors. `release` needs `ci`, `linux` and `macos`; the `docker` test job is
independent of it, while `docker-publish` also waits for `docker` and `release`.

The `ci` job also compiles the winget variant of the agent setup program, so a change to
`scripts/installer/gwatch-agent.iss` that breaks it fails a pull request rather than a tag.

Pushing a tag such as `v0.1.0` additionally runs the `release` job (below) and the
`docker-publish` job (["The container image"](#the-container-image)); pushing
`agent-v0.5.0` runs `agent-release` and then the four agent packaging jobs
(["The agent packages"](#the-agent-packages)); and pushing
`mcp/v0.1.0` runs `mcp-tag`, a guard job — see
["Releasing the MCP companion"](#releasing-the-mcp-companion).

## Repository layout

| Path | Purpose |
|---|---|
| `main.go` | CLI, cross-platform service wrapper (kardianos/service), HTTP server bound to localhost by default |
| `migrate_db.go` | `gwatch migrate-db`: copies a SQLite install into a PostgreSQL/MySQL database |
| `internal/model` | Shared data types and JSON wire format |
| `internal/dbconfig` | Which database to open: flags, `GWATCH_DB_*`, `database.json`, then the SQLite default |
| `internal/store` | Schema, migrations, single queued writer, rollups and history queries, over a dialect layer that speaks SQLite, PostgreSQL and MySQL/MariaDB |
| `internal/checks` | Check runners: ping, http, cert, tcp, dns, keyword, json, custom, system (hardware) and snmp; node templates |
| `internal/engine` | Scheduler, result processing, alert rules, dependencies, maintenance, retention, health, triggers, notification rules |
| `internal/actions` | Automation actions: HTTP requests, git commands, custom scripts, run-node, and the Slack / Teams / ntfy / Pushover notifiers |
| `internal/auth` | Roles, principals, password hashing, sessions and API-key digests |
| `internal/secrets` | The envelope that seals individual configuration values with the machine-local `gwatch.key` |
| `internal/discovery` | Subnet sweeps: what answers, what it is called, which ports are open |
| `internal/hostmon` | The newest hardware reading of each machine, in memory: samples this machine, accepts agent pushes (the engine then has the machine's check judge them) |
| `internal/sysmetrics` | The platform-tagged collectors behind `hostmon` (`collect_linux.go`, `collect_darwin.go`, `collect_windows.go`) |
| `internal/update` | GitHub release check, download, checksum, signature verification and executable swap |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/logging` | The size-rotated file + in-memory service log behind `/api/logs` |
| `internal/api` | JSON API (see [`API.md`](API.md)) and static UI serving |
| `cmd/gwatch-agent` | The one-directional hardware agent installed on other machines ([`HARDWARE.md`](HARDWARE.md)) |
| `cmd/gwatch-sign` | Maintainer CLI: generate the release signing key, sign and verify release assets |
| `cmd/gwatch-rsrc/` | Builds the `.syso` resource objects that put the icons inside the Windows executables — GWatch's mark in `gwatch.exe`, the agent's in `gwatch-agent.exe` (`make rsrc`) |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary; open with `?mock=1` for an in-browser demo backend) |
| `web/fonts/` | Barlow and Kode Mono, latin subsets, self-hosted so the UI still requests nothing from the internet ([SIL OFL 1.1](../web/fonts/OFL.txt)) |
| `tests/web/` | The jsdom suite for `web/` (`make web-test`). It lives outside `web/` so `//go:embed` never ships it |
| `tests/e2e/` | Playwright + axe over every route, in both themes (`make web-e2e`); `playwright.config.mjs` and `package.json` configure both suites |
| `skill/` | The downloadable agent skill served by `GET /api/mcp/skill`, versioned by `skill/VERSION` |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |
| `scripts/installer/` | The two Inno Setup scripts, their shared branding and the wizard artwork |
| `packaging/` | The agent's packages: `build-agent.sh` (the packaged builds), `nfpm/` (`.deb`/`.rpm`, systemd unit, maintainer scripts), `homebrew/`, `winget/` and `docker/Dockerfile.agent` — see ["The agent packages"](#the-agent-packages) |
| `VERSION` | The version every build reports; a release is the tag `v<VERSION>` |
| `mcp/` | The MCP companion, a separate Go module with its own `VERSION` — see [`mcp/README.md`](../mcp/README.md) |
| `Dockerfile`, `.dockerignore`, `docker-compose.yml` | The container image (`make docker`) and a ready-to-run Compose file — see [`DOCKER.md`](DOCKER.md) |

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
3. cross-compiles `gwatch-<os>-<arch>[.exe]` for Windows, Linux and macOS,
   Authenticode-signs the Windows executables when that is configured (see
   ["Authenticode signing"](#authenticode-signing)), and then writes a `.sha256` next to
   each;
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
make sign KEY=release.key      # writes dist/<asset>.sig, for release VERSION
make verify-release            # checks them against release_keys.txt
# an agent release: add RELEASE_VERSION=$(cat cmd/gwatch-agent/VERSION) to both
```

### Release candidates

Tags with a prerelease suffix (for example `v0.7.0-rc.1` or
`agent-v0.7.0-rc.1`) publish GitHub prereleases and do not move Docker's `latest`
tag. The updater also recognizes the suffix independently of GitHub's flag,
so stable-channel clients skip accidentally unflagged release candidates.

## Signature format

Each asset gets a sibling `<asset>.sig`:

```
gwatch-sig-v1
key: 1a2b3c4d
sig: <base64 ed25519 signature over the digest>
asset: gwatch-linux-amd64
version: 1.2.3
release-sig: <base64 ed25519 signature over the release message>
```

`key` is the first 4 bytes of `sha256(public key)`, in hex — a hint for humans
and logs, not a credential. Both signatures cover the **SHA-256 digest** of the
asset rather than its bytes, so the signer and the updater both stream the file
and hold only 32 bytes in memory.

`release-sig` is the signature current builds and the install scripts check. It
is made over

```
gwatch-release-v1
asset: <asset name as published>
version: <release version, without the tag prefix>
sha256: <hex digest>
```

so a signed binary only verifies under the exact name and version it was
released as: an older agent cannot be republished as a newer one, and an agent
cannot be renamed as a GWatch build. The updater compares that version with the
release it was offered, and refuses an asset whose signature names anything
else — before downloading the asset. `gwatch-sign sign` and `verify` therefore
take `-version` (the release workflow passes the planned version).

`sig` covers the digest alone. It is still written only so copies released
before `release-sig` existed (0.7.0 and older) can install the release that
brings them the check; they ignore the lines they do not know. Once no such
copies are in use, `sig` can be dropped.

Updates are also capped at 512 MiB and at the size the release declares, so a
hostile release cannot fill the disk before the signature check.

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

| Script | Built by | From | Published as |
|---|---|---|---|
| `scripts/installer/gwatch.iss` | `release` (`v*`) | `dist/gwatch-windows-amd64.exe` | `gwatch-setup-<version>.exe` |
| `scripts/installer/gwatch-agent.iss` | `agent-release` (`agent-v*`) | `dist/gwatch-agent-windows-amd64.exe` | `gwatch-agent-setup-<version>.exe` |

The two are built by different jobs because the agent releases on its own
([below](#releasing-the-agent)); every push and pull request still compiles
both, in the `ci` job, so neither script can rot unnoticed.

Both are compiled with `iscc /DAppVersion=<tag> /DExePath=<exe>` — see
[`scripts/installer/README.md`](../scripts/installer/README.md) for what they do and how
to build them locally. Three things to keep in mind:

- Both are **Authenticode-signed**, with the executables inside them and their
  uninstallers, once the release's signing account is configured — and built unsigned,
  with a warning, until then. See ["Authenticode signing"](#authenticode-signing).
- Neither is picked up by the in-app self-updater. `pickAsset` (in
  `internal/update`) only matches `gwatch-<os>-<arch>[.exe]`, so `*-setup-*.exe`
  is invisible to it by design — an installer is a first-install/reinstall path, not an
  update payload. The updater keeps managing the plain `gwatch.exe` binary in place as
  it does today.
- They have no `.sha256` of their own. They are outside the self-update chain, though
  the signing job's `dist/gwatch-*` glob does give them an ed25519 `.sig`, made over the
  Authenticode-signed file like everything else.

The wizard artwork under `scripts/installer/assets/` is committed, not generated at
release time, so a release needs no image tooling on the runner. Both marks come
from the logo kits in [`brand/`](../brand/README.md); if one changes, rebuild the
kit, copy its files into `web/` and the masters into `scripts/installer/assets/`,
then run `python3 scripts/installer/make-assets.py` and `make rsrc` (see
[`scripts/installer/README.md`](../scripts/installer/README.md#branding-assets)).
Both installers are light-themed only.

## Authenticode signing

Windows checks a second kind of signature that has nothing to do with the updater's: an
**Authenticode** signature from a code-signing certificate, which is what SmartScreen and
managed machines' application-control policies look at. Without one, SmartScreen says
"unknown publisher" and plenty of company machines refuse to run the setup program at all.
The release workflow adds one with
[Azure Artifact Signing](https://learn.microsoft.com/azure/artifact-signing/) (called
*Trusted Signing* until late 2025), when it is configured, and builds exactly as before
when it is not. The ed25519 update signatures are unchanged and still required; Authenticode
is in addition to them.

### What is signed, and in what order

| File | Job |
|---|---|
| `gwatch-windows-amd64.exe`, `gwatch-windows-arm64.exe` | `gwatch-build` |
| `gwatch-setup-<v>.exe`, and the uninstaller it installs (`unins000.exe`) | `gwatch-build` |
| `gwatch-agent-windows-amd64.exe`, `gwatch-agent-windows-arm64.exe` | `gwatch-agent-build` |
| `gwatch-agent-setup-<v>.exe`, and its uninstaller | `gwatch-agent-build` |
| the winget build of `gwatch-agent.exe`, `gwatch-agent-winget-setup-<v>.exe` and its uninstaller | `agent-winget` |

(`gwatch-mcp-windows-*.exe` is not signed yet. Adding it is one more path on the signing
step's command line.)

Signing an `.exe` changes its bytes, so **the order matters**. Each Windows job:

1. builds the executables;
2. **Authenticode-signs** them (`scripts/authenticode.ps1 -Action Sign`);
3. compiles the setup program with Inno Setup, which packages the *signed* executable and,
   through `SignTool=` and `SignedUninstaller=yes` in the `.iss`, signs the uninstaller it
   embeds and then the finished setup program;
4. **verifies** every one of those files with `signtool verify /pa /v` *and*
   `Get-AuthenticodeSignature` (status `Valid`). Either one failing fails the release;
5. only then writes the `.sha256` files (and, in `agent-winget`, renders the winget
   manifest, whose `InstallerSha256` is the signed setup program's).

The ed25519 `.sig` files come later still, in `gwatch-sign` / `gwatch-agent-sign`, from the
artifact those jobs upload, and the release's `SHA256SUMS.txt` is computed when the bundle
is staged. Everything that describes a file therefore describes the signed file. Never
re-sign a file after that point — not by hand after a release is published either: its
checksum and `.sig` would stop matching and installed copies would refuse the update. CI's
`linux` job runs `scripts/ci/check-authenticode-order.py` over the release workflow to keep
this order from being rearranged by accident.

### When it is not configured

The `Authenticode configuration` step of each Windows job reads the variables below.

- **None set** (forks, and this repository until the account exists): the release goes on
  unsigned, byte for byte as before, with a `::warning` annotation and a
  "Windows binaries are not Authenticode-signed" section in that job's summary.
- **Some set and some not:** the release fails at that step and names the missing ones. A
  half-configured signer is a mistake, not a decision to ship unsigned.
- **All set:** the files above are signed and verified, and the job summary names the
  signer of each. A signing or verification failure fails the release.

CI's Windows job runs `scripts/ci/authenticode-dry-run.ps1`, with no secrets: it takes the
configuration step through all three cases, then runs the signed path end to end with a
throwaway self-signed certificate — both executables, both setup programs (checking that
Inno really called the signer for the uninstaller), and the same verification the release
does.

### The variables

There are **no secrets**: the workflow signs in to Azure with GitHub's OIDC token
(`azure/login`, pinned by commit), so there is no password or certificate to store or
rotate. These are Actions **variables**, set on the `authenticode` environment
(*Settings → Environments → authenticode → Environment variables*) or, less tightly, on the
repository.

| Variable | What goes in it | Where to find it |
|---|---|---|
| `AZURE_SIGNING_CLIENT_ID` | The *Application (client) ID* of the Entra ID app registration the workflow signs in as | Entra ID → App registrations → the app → Overview |
| `AZURE_SIGNING_TENANT_ID` | The *Directory (tenant) ID* | The same page |
| `AZURE_SIGNING_ENDPOINT` | The Artifact Signing account's regional endpoint, e.g. `https://eus.codesigning.azure.net` (it must match the account's region, or signing fails with 403) | The signing account → Overview → *Account URI* |
| `AZURE_SIGNING_ACCOUNT` | The Artifact Signing account's name | The signing account → Overview |
| `AZURE_SIGNING_PROFILE` | The certificate profile's name | The signing account → Certificate profiles |
| `AZURE_SIGNING_SUBSCRIPTION_ID` | Optional: the subscription that holds the account. Leave it unset and `azure/login` signs in to the tenant without one | Subscriptions |

### One-time setup

1. **An Azure subscription with the provider registered.** In the subscription,
   *Settings → Resource providers*, register `Microsoft.CodeSigning`.
2. **Create the signing account.** In the portal, create an *Artifact Signing account*
   (the Basic SKU is enough for this volume) in a region near you; its region decides the
   endpoint.
3. **Validate the publisher's identity.** Give yourself the *Artifact Signing Identity
   Verifier* role on the account (it was *Trusted Signing Identity Verifier* before the
   rename), then under *Identity validations* create a **Public Trust** validation —
   *Organization* for JX Holdings, LLC, which needs the company's registration details and
   can take several business days. The certificate's subject, and so the "Verified
   publisher" Windows shows, is the validated name.
4. **Create a certificate profile** of type *Public Trust* on that validation. Its name is
   `AZURE_SIGNING_PROFILE`. Microsoft issues the actual certificates from it, each valid for
   a few days and rotated automatically; the workflow timestamps every signature
   (`http://timestamp.acs.microsoft.com`), so signatures stay valid after the certificate
   that made them expires.
5. **Create the identity the workflow signs in as.** In Entra ID, register an application
   (for example `gwatch-release-signing`; no redirect URI, no client secret). Under
   *Certificates & secrets → Federated credentials*, add one for *GitHub Actions deploying
   Azure resources*: organisation `jxburros`, repository `GWatch`, entity type
   **Environment**, environment `authenticode`. That makes the subject
   `repo:jxburros/GWatch:environment:authenticode` with audience
   `api://AzureADTokenExchange`, so only jobs of this repository that run in that
   environment can sign in as it. (A fork does this with its own names.)
6. **Let it sign.** On the certificate profile (or the account), give that application the
   *Artifact Signing Certificate Profile Signer* role (*Trusted Signing Certificate Profile
   Signer* before the rename). It needs nothing else.
7. **Create the `authenticode` environment** in GitHub (*Settings → Environments*), restrict
   its deployment branches to `main` (releases are dispatched from `main`), and add the
   variables above to it. A required reviewer is possible but makes every Windows build job
   wait for approval, on top of the `release` environment's approval of the ed25519
   signing jobs; the federated credential and the branch restriction are what keep other
   workflows out.
8. **Dry-run it.** Dispatch the Release workflow with `dry_run` on. Each Windows job's
   summary should list its files and their signer; download a setup program from the run's
   artifacts and check *Properties → Digital Signatures* on Windows.

The jobs use the runner's Windows SDK `signtool.exe` and the Artifact Signing client
("dlib") from NuGet, pinned by version and SHA-256 in `scripts/authenticode.ps1`; it needs
the .NET 8 runtime, which `windows-latest` has. To move the client to a newer version,
change the version and hash there together.

SmartScreen reputation is earned per publisher over time: the first signed releases may
still show a (milder) prompt until enough people have run them, but they name the publisher
instead of "unknown", and managed machines that allow by publisher will run them.

**winget.** The manifest's `InstallerSha256` is computed in `agent-winget` after the setup
program is signed, so the published hash and the published file agree; nothing in the
manifest names the certificate, so the first signed release needs no manifest change. The
winget-pkgs validation pipeline scans submitted installers, and a signed one is less likely
to be held for manual review. If a signed setup program is ever replaced on the release
page, the manifest's hash must be updated with it, or `winget install` refuses the file.

**Signing locally.** `scripts/build-installer.ps1` signs too when
`GWATCH_SIGNING_CERT_THUMBPRINT` names a code-signing certificate in
`Cert:\CurrentUser\My` (set `GWATCH_SIGNING_TIMESTAMP_URL` to timestamp it). That is for a
certificate you hold yourself, an OV/EV certificate on a token for example; a cloud HSM
service such as SSL.com eSigner or DigiCert KeyLocker would replace the Artifact Signing
branch of `scripts/authenticode.ps1` the same way.

## Releasing the agent

`gwatch-agent` ([`docs/HARDWARE.md`](HARDWARE.md)) has its own version, in
[`cmd/gwatch-agent/VERSION`](../cmd/gwatch-agent/VERSION), and its own tag
scheme, `agent-v<version>`. It is part of the root module — unlike the MCP
companion, there is no `go install` story to satisfy — so the separate tag
exists for a different reason: **the agent updates itself**, and a thing that
updates itself needs a release train it can be pointed at.

The agent runs on machines nobody logs into. If its version were the server's,
every agent fix would need a GWatch release, and every GWatch release would
restart every agent. Separating them means the agent can be fixed on its own
schedule, and a GWatch release stops being an event on fifty other machines.

To cut an agent release:

```sh
# 1. Bump the version and commit
echo 0.5.0 > cmd/gwatch-agent/VERSION
git commit -am "Release gwatch-agent 0.5.0"
git push

# 2. Tag it, on the same commit
git tag agent-v0.5.0
git push origin agent-v0.5.0
```

Pushing an `agent-v*` tag runs the `agent-release` job in
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml). It fails if the tag
does not match `cmd/gwatch-agent/VERSION`, fails if the signing key is missing
or is not one the shipped agents trust, then cross-compiles seven targets
(the six the server is built for, plus `linux/arm` for the Raspberry Pi class
of machine), signs and checksums each one, builds the Windows agent installer,
and publishes a GitHub release of its own. Once it has, four more jobs add the
agent's packages to that release — see ["The agent packages"](#the-agent-packages).

### The two release trains must never cross

A GWatch installation and an agent read the *same* repository's releases and
must never be offered each other's build — an agent that installed a GWatch
server over itself would take out both the machine's monitoring and the way to
repair it. Two things keep them apart, and both are tested
(`TestReleaseFamiliesStayApart`, `TestCheckUpdateOnlySeesAgentReleases`):

| | Tags | Assets |
|---|---|---|
| GWatch | `v1.2.3` | `gwatch-<os>-<arch>[.exe]` |
| Agent | `agent-v1.2.3` | `gwatch-agent-<os>-<arch>[.exe]` |

`internal/update.Client` carries a `TagPrefix` and an `AssetPrefix`; releases
whose tag is not this family's are skipped entirely, and asset names are
matched exactly, so `gwatch-agent-linux-amd64` can never satisfy a request for
`gwatch-linux-amd64`. Keep that true when adding anything else to a release.

### What reaches machines, and when

Agents left on automatic updates (the default) check every few hours, with the
check spread over a window so a fleet installed by one script does not ask, or
restart, in lockstep. An agent installs a release only if the download verifies
against a signing key pinned into the agent binary itself; **GWatch is never
asked and cannot tell an agent to install anything**. That is deliberate: a
compromised GWatch must not become a way to run code on every machine that
reports to it.

Before replacing itself, an agent makes the download prove it runs on that
machine and that it can take a reading the server accepts. The binary it
replaces is kept beside it as `.old`, so `gwatch-agent rollback` is a repair
that needs no network. There is no automatic rollback *after* a restart — that
would need a watchdog the agent deliberately does not have — so an agent
release is the one release worth being slow about. Tag it, let it reach your
own machines, and look at them before you expect anyone else to take it.

## The agent packages

The agent is also published as a `.deb`, an `.rpm`, a Homebrew formula, a winget package
and a container image ([`HARDWARE.md`](HARDWARE.md#installing-from-a-package-manager)).
Each is a job on the `agent-v*` tag that runs after `agent-release` (so the release exists
to attach to, and a failed release publishes no packages) and after `agent-packaging`, the
pull-request job that built and exercised the same packages:

| Job | Runner | Publishes | Needs |
|---|---|---|---|
| `agent-linux-packages` | `ubuntu-latest` | `gwatch-agent_<v>-1_{amd64,arm64,armhf}.deb`, `gwatch-agent-<v>-1.{x86_64,aarch64,armv7hl}.rpm` and `gwatch-agent-linux-packages-<v>.sha256` on the agent release | nothing |
| `agent-homebrew` | `ubuntu-latest` | `gwatch-agent-homebrew-<v>-{darwin,linux}-{arm64,amd64}.tar.gz` (+ `.sha256`) on the release; `Formula/gwatch-agent.rb` to the tap | `HOMEBREW_TAP_TOKEN` to push the formula |
| `agent-winget` | `windows-latest` | `gwatch-agent-winget-setup-<v>.exe` and `gwatch-agent-winget-manifests-<v>.zip` on the release; a pull request to `microsoft/winget-pkgs` | `WINGET_TOKEN` to open the pull request |
| `agent-docker-publish` | `ubuntu-latest` | `ghcr.io/jxburros/gwatch-agent:<v>`, `:<major.minor>`, `:latest` for `linux/amd64`, `linux/arm64`, `linux/arm/v7` | nothing beyond `GITHUB_TOKEN` |

Each repeats the tag-matches-`cmd/gwatch-agent/VERSION` guard. The two jobs with an
optional secret **skip that one step, with a notice, when the secret is not set** — the
formula and the manifests are then attached to the run as artifacts for doing by hand —
so the packages can ship before the tap or the winget listing exists.

**Packaged binaries are different binaries.** Every package is built with
`-X main.packagedBy=<deb|rpm|homebrew|winget|docker>` by `packaging/build-agent.sh`, which
makes the agent leave updating to its package manager (it refuses to replace itself and
says which command to run — [`AGENT-DECISIONS.md`](AGENT-DECISIONS.md#10-a-packaged-agent-belongs-to-its-package-manager-it-does-not-update-itself)).
So the jobs build the agent again rather than reusing `agent-release`'s binaries, and no
package, and no binary inside one, is ever uploaded under a `gwatch-agent-<os>-<arch>` name
— the only names the self-updater takes. That is tested
(`TestPackagedAssetsAreInvisibleToTheUpdater`) and checked on every PR.

**The signing key stays where it was.** None of these jobs is given `GWATCH_SIGNING_KEY`.
The packages are not in the signed self-update chain — their integrity is the sha256
their package manager pins, computed from the uploaded file by `packaging/*/render.sh` —
and keeping the key in the one job that signs means a compromised packaging tool could
publish a bad package, but could not sign an update that every self-updating agent would
install unattended.

### One-time setup for the packages

1. **Homebrew tap.** Create the public repository `jxburros/homebrew-tap` (the
   `homebrew-` prefix is what makes `brew install jxburros/tap/gwatch-agent` work), with a
   `Formula/` directory or nothing at all. Create a fine-grained personal access token
   with *Contents: read and write* on that repository only, and store it as the
   `HOMEBREW_TAP_TOKEN` Actions secret. A fork publishing to a different tap sets the
   `HOMEBREW_TAP_REPO` Actions variable (`owner/homebrew-name`).
2. **winget.** Create a classic personal access token with the `public_repo` scope on the
   account that will own the fork of `microsoft/winget-pkgs` (`wingetcreate` forks it on
   first use), and store it as the `WINGET_TOKEN` secret. The first submission of
   `GWatch.Agent` is reviewed by the winget-pkgs moderators like any new package; later
   versions are ordinary update pull requests. Without the secret, submit the manifests
   from the release zip with `wingetcreate submit <dir>` or by hand.
3. **Container package.** After the first push, make `gwatch-agent` public in the
   repository's Packages settings, as for the server image.
4. **Linux packages** need nothing: they are release assets. There is no apt or dnf
   repository, which is why a packaged Linux agent does not update unattended; a signed
   repository is the follow-up that would change that.

The `.deb`'s `Maintainer` field is `JX Holdings, LLC <https://github.com/jxburros/GWatch>`
(`packaging/nfpm/nfpm.yaml`); put a real contact address there if one is wanted.

## The server packages

The server is also published as a `.deb` and an `.rpm` for amd64, arm64 and armhf
([`INSTALL-LINUX.md`](INSTALL-LINUX.md#from-a-deb-or-rpm-package)). `server-linux-packages`
runs in a GWatch release after `gwatch-build` and `server-packaging` (the pull-request job
that built and exercised the same packages), checks the tag against `VERSION`, builds them
with `packaging/nfpm/gwatch/package.sh` and hands `gwatch_<v>-1_{amd64,arm64,armhf}.deb`,
`gwatch-<v>-1.{x86_64,aarch64,armv7hl}.rpm` and `gwatch-linux-packages-<v>.sha256` to
`assemble-gwatch`, which publishes them with the rest of the release (they are required
assets in `.release/config.json`). `make server-packages` builds the same files locally.

As with the agent, the packaged binaries are different binaries: `packaging/build-server.sh`
adds `-X main.packagedBy=deb` (or `rpm`), which makes Settings › Updates report releases
without installing them and `gwatch install`/`uninstall` defer to the package's unit. They
are never uploaded under a `gwatch-<os>-<arch>` name, the job is not given
`GWATCH_SIGNING_KEY`, and the packages are outside the signed self-update chain.

There is no apt or dnf repository yet, so a packaged server is updated by installing the next
release's file. Hosting a signed repository needs an external account and a repository key,
and is a follow-up.

## The container image

The same `v*` tag also runs `docker-publish`, which builds the `Dockerfile` for
`linux/amd64`, `linux/arm64` and `linux/arm/v7` and pushes the result to GitHub's registry as
`ghcr.io/jxburros/gwatch`, tagged `X.Y.Z`, `X.Y` and `latest`. It runs beside `release`
rather than inside it — a separate runner (Linux, with buildx and QEMU), a separate
permission (`packages: write`, granted to that job only) and the same tag-matches-VERSION
guard, so a mismatch stops both. Nothing else needs setting up: it authenticates with
the workflow's own `GITHUB_TOKEN`, and the first push creates the package. After that
first push, make the package public in the repository's Packages settings, or a
`docker pull` without a login fails.

The image is not in the signed self-update chain either. The binary inside it carries
the version from the tag like every other artefact, but the in-app updater cannot
replace an executable in a read-only image directory and says so; container users
upgrade by pulling ([`DOCKER.md`](DOCKER.md#upgrading)). The image's own integrity
comes from the registry's digest, not from a `.sig`.

## Releasing the MCP companion

`gwatch-mcp` ([`mcp/README.md`](../mcp/README.md)) is a separate Go module
(`mcp/go.mod`) with its own version, in [`mcp/VERSION`](../mcp/VERSION). It
does not share the core `VERSION` file or the core `v<VERSION>` tag scheme —
it uses **nested-module tags**, `mcp/v<VERSION>`, which is the scheme Go's
module resolution expects for a module that lives in a subdirectory of the
repository: it is what makes

```sh
go install github.com/jxburros/GWatch/mcp/cmd/gwatch-mcp@latest
```

resolve to the right code. A plain `v<VERSION>` tag on the root module is
invisible to `go install` for a path under `mcp/`.

To cut an MCP release:

```sh
# 1. Bump the version and commit
echo 0.2.0 > mcp/VERSION
git commit -am "Release gwatch-mcp 0.2.0"
git push

# 2. Tag it, on the same commit
git tag mcp/v0.2.0
git push origin mcp/v0.2.0
```

Pushing an `mcp/v*` tag runs the `mcp-tag` job in
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml): it fails if the
tag does not match `mcp/VERSION`, and builds the module, but it does **not**
publish a separate GitHub release. `gwatch-mcp` binaries ship inside the
*core* release instead — the `release` job's "Cross-compile the MCP
companion" step builds `gwatch-mcp-<os>-<arch>[.exe]` from whatever
`mcp/VERSION` says at the time a core `v*` tag is pushed, signs and
checksums it alongside everything else, and attaches it to that release. In
practice this means: bump `mcp/VERSION` and push an `mcp/v*` tag whenever
`mcp/` changes and you want `go install ...@latest` to see it, and expect the
built binary to actually reach users on the *next* core release rather than
immediately — the two release trains are independent, and the mcp tag exists
for `go install`, not for GitHub's release page.

Bump `mcp/VERSION` (and push a matching tag) for any change under `mcp/`
that a `go install` user should see reflected in `gwatch-mcp version` and in
module resolution — not just user-visible tool changes, but also fixes to
the HTTP client, the MCP transport layer or the SDK pin.

### The agent skill

[`skill/SKILL.md`](../skill/SKILL.md) is the document an assistant reads to
learn how to use the MCP tools well. It is embedded into the core binary
(`//go:embed skill` in `main.go`) and handed out by **Settings › AI & MCP**,
which also remembers who last downloaded which version and mentions when the
embedded copy has moved on since.

It has its own version, in [`skill/VERSION`](../skill/VERSION) and repeated in
the front matter of `SKILL.md`, independent of both `VERSION` and
`mcp/VERSION`. **Bump it whenever `SKILL.md` changes in a way users should
re-download** — new guidance, a new tool, a corrected instruction. Typo fixes
need not bump it. No tag is involved: the version is read from the embedded
file at run time, so a bumped `skill/VERSION` reaches users with the next core
release and nothing else has to happen. Keep the two copies of the number in
step.

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

## Protected publishing

Signing jobs require the `release` GitHub Environment, verify their commit is an ancestor of `origin/main`, disable build-cache restores and use actions pinned to commits. Configure that environment to require a reviewer and restrict deployment tags to `v*` and `agent-v*`. Move `GWATCH_SIGNING_KEY` from repository secrets into this environment and remove the repository copy; an environment declaration alone cannot protect a remaining repository secret. GitHub never reveals an existing secret. If the offline seed is unavailable, the main-only `Migrate release signing secret` workflow seals it directly to the release Environment public key, producing only a ciphertext envelope. Apply that JSON through the Environment secrets API, confirm the destination exists, then delete the repository secret. The environment reviewer must verify the destination public key and key id against the API before approving. Optional Homebrew/winget tokens are available only to their publishing steps, and checkouts do not persist credentials. Container publication waits for the signed release. Weekly CI includes vulnerability scans.

The Linux job runs staticcheck, all root race tests, real-server browser tests and shell installer lint. `make ci` is the local subset; use `make test-postgres`, `make test-mysql` and the alternative SQLite build/test targets for backend coverage. Installers are published with their respective core/agent releases.


### Protected signing-key migration

Create the `release` Environment with required maintainer review and selected deployment
branches/tags (`main` for this one-time migration, `v*` and `agent-v*` for releases).
Get its secrets public key and key ID from the GitHub API. Dispatch
`migrate-signing-secret.yml` on main with those two public values. Approve the Environment
job after checking that the values name this repository's Environment. The artifact
contains only an anonymous sealed-box ciphertext, which only GitHub's Environment key
can decrypt. PUT the artifact JSON to the Environment's `GWATCH_SIGNING_KEY` secret
endpoint, confirm it exists, then delete the repository-level copy. Never print or
download the plaintext key. The migration helper cannot decrypt secrets.

`?mock=1` is enabled only by the development server's `/__gwatch_dev__` marker;
production binaries ignore it. Regenerate template fixtures with
`go run ./tests/e2e/generate-fixtures.go`; CI browser tests run axe contrast checks in
both explicitly verified themes and exercise the real service and MCP companion.
