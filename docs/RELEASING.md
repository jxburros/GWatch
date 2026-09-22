# Releasing GWatch

GWatch updates itself in place, so a release is only useful if the running
binary can prove the file it just downloaded came from you. Every release asset
is signed with an ed25519 key whose public half is compiled into the binary; an
update that does not verify is refused, not installed with a warning.

## Building and testing locally

```bash
make test                         # go vet + unit and integration tests (SQLite)
make ci                           # everything CI runs: gofmt, vet, go mod tidy, race tests,
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
request as four jobs:

| Job | Runner | What it does |
|---|---|---|
| `ci` ("Lint, build, test (Windows)") | `windows-latest` | gofmt, vet, `go mod tidy`/`verify`, build + full test suite, the mcp/ module, web-asset `node --check` plus the jsdom suite (`npm test`, `tests/web/`), PowerShell script parsing, and compiles both Inno Setup installers |
| `linux` ("Test (Linux)") | `ubuntu-latest` | gofmt, vet, build, full test suite (root and mcp/), a `-race` pass over `internal/engine`, `internal/api`, `internal/store` and `internal/hostmon`, the store/backup/api suites against PostgreSQL 16 and MySQL 8 service containers ([`DATABASE.md`](DATABASE.md#for-developers)), `govulncheck` for both modules, and the browser accessibility suite (`npm run test:e2e` — Playwright drives Chromium through every route with an axe-core scan) |
| `macos` ("Test (macOS)") | `macos-latest` | vet + test only — deliberately lean, but this is what actually compiles and exercises `internal/sysmetrics/collect_darwin.go` |
| `docker` ("Container image") | `ubuntu-latest` | builds the `Dockerfile` for `linux/amd64`, checks `gwatch version` inside it, then starts the container and waits for `/api/health` to answer 200 |

Windows is the only one that builds an installer or touches PowerShell, since that is the
only platform GWatch installs itself onto as a service; Linux and macOS exist to catch a
platform-specific regression (a build tag, a syscall, a platform-tagged file the Windows
job never compiles) before it reaches a tag push. `release` needs `ci`, `linux` and `macos`; the `docker` job is independent of it.

Pushing a tag such as `v0.1.0` additionally runs the `release` job (below) and the
`docker-publish` job (["The container image"](#the-container-image)), and pushing
`mcp/v0.1.0` runs `mcp-tag`, a guard job — see
["Releasing the MCP companion"](#releasing-the-mcp-companion).

## Repository layout

| Path | Purpose |
|---|---|
| `main.go` | CLI, Windows service wrapper (kardianos/service), HTTP server bound to localhost |
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
| `internal/hostmon` | Hardware readings: samples this machine, accepts agent pushes, answers the hardware API |
| `internal/sysmetrics` | The platform-tagged collectors behind `hostmon` (`collect_linux.go`, `collect_darwin.go`, `collect_windows.go`) |
| `internal/update` | GitHub release check, download, checksum, signature verification and executable swap |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/logging` | The size-rotated file + in-memory service log behind `/api/logs` |
| `internal/api` | JSON API (see [`API.md`](API.md)) and static UI serving |
| `cmd/gwatch-agent` | The one-directional hardware agent installed on other machines ([`HARDWARE.md`](HARDWARE.md)) |
| `cmd/gwatch-sign` | Maintainer CLI: generate the release signing key, sign and verify release assets |
| `cmd/gwatch-rsrc/` | Builds the `.syso` resource objects that put the GWatch icon inside the Windows executables (`make rsrc`) |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary; open with `?mock=1` for an in-browser demo backend) |
| `web/fonts/` | Barlow and Kode Mono, latin subsets, self-hosted so the UI still requests nothing from the internet ([SIL OFL 1.1](../web/fonts/OFL.txt)) |
| `tests/web/` | The jsdom suite for `web/` (`make web-test`). It lives outside `web/` so `//go:embed` never ships it |
| `tests/e2e/` | Playwright + axe over every route, in both themes (`make web-e2e`); `playwright.config.mjs` and `package.json` configure both suites |
| `skill/` | The downloadable agent skill served by `GET /api/mcp/skill`, versioned by `skill/VERSION` |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |
| `scripts/installer/` | The two Inno Setup scripts, their shared branding and the wizard artwork |
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
and publishes a GitHub release of its own.

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

## The container image

The same `v*` tag also runs `docker-publish`, which builds the `Dockerfile` for
`linux/amd64` and `linux/arm64` and pushes the result to GitHub's registry as
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
