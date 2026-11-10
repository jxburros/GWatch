# Developing GWatch

How to build GWatch from source, run its tests, and find your way around the
repository. Cutting a release is in [`RELEASING.md`](RELEASING.md); how to propose
a change is in [`CONTRIBUTING.md`](../../CONTRIBUTING.md).

## Running from source

You need Go 1.26. `go.mod` names 1.26.0 as the minimum, but build with the newest
1.26 patch release: it carries the standard library's security fixes, and it is what
CI and the released binaries use. Node is needed only for the web tests.

```bash
go run . run --data-dir ./data
```

Then open <http://127.0.0.1:7230>. Monitoring runs only while this process runs.
`go run . --help` lists the other commands and flags (`--listen` or `GWATCH_LISTEN`
changes the address). The web interface is plain JavaScript served from `web/` with
no build step; `/?mock=1` runs it against the mock backend in `web/mock.js`.

## Building and testing locally

```bash
make test                         # go vet + unit and integration tests (SQLite)
make ci                           # local checks (CI also tests platform/backend integrations): gofmt, vet, go mod tidy, race tests,
                                  #   the mcp/ module, the jsdom web suite and the Playwright/axe suite
make cover                        # race tests plus a per-function coverage report
make test-postgres / test-mysql   # the store, backup, API, engine and hostmon suites against a server
make web-check                    # node --check over every web/*.js (needs node on PATH)
make web-test                     # the jsdom suite in tests/web/
make web-typecheck                # tsc over the web/ files marked // @ts-check (see tsconfig.json)
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
from fixtures, so the tests are deterministic on a CI runner. See the [`Makefile`](../../Makefile)
for what each target actually runs.

By default every test runs on SQLite. The store, backup, API, engine and hostmon suites
also run against a PostgreSQL or MySQL server when `GWATCH_TEST_DB` and a DSN are set —
see [`DATABASE.md`](../DATABASE.md#for-developers); CI does this on the Linux job with
service containers.

## Continuous integration

[`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) runs on every push and pull
request with the following jobs:

| Job | Runner | What it does |
|---|---|---|
| `ci` ("Lint, build, test (Windows)") | `windows-latest` | gofmt, vet, `go mod tidy`/`verify`, build + full test suite, the mcp/ module, web-asset `node --check` plus the jsdom suite (`npm test`, `tests/web/`) and the web type check (`npm run typecheck`), PowerShell script parsing, compiles both Inno Setup installers, and dry-runs the release's Authenticode signing with a throwaway certificate ([below](RELEASING.md#authenticode-signing)) |
| `linux` ("Test (Linux)") | `ubuntu-latest` | gofmt, vet, staticcheck, build, full test and race suites (root and mcp/), the migration/store/backup/api/engine/hostmon suites against PostgreSQL 16 and MySQL 8 service containers ([`DATABASE.md`](../DATABASE.md#for-developers)), `govulncheck` for both modules, and the browser accessibility suite (`npm run test:e2e` — Playwright drives Chromium through every route with an axe-core scan) |
| `macos` ("Test (macOS)") | `macos-latest` | vet + test only — deliberately lean, but this is what actually compiles and exercises `internal/sysmetrics/collect_darwin.go` |
| `docker` ("Container image") | `ubuntu-latest` | builds the `Dockerfile` for `linux/amd64` and `linux/arm/v7`, checks `gwatch version` inside it, then starts the container and waits for `/api/health` to answer 200 |
| `server-packaging` ("Server packages") | `ubuntu-latest` | builds the server's `.deb`/`.rpm` for all three architectures and checks the `.deb`'s contents, then `scripts/ci/linux-service-test.sh package`: install, the same service assertions as `linux-service` (exposure at most 2.0), refuse to self-update or register a second service, upgrade to a newer `.deb` with the data intact, remove (data kept) and purge (nothing left) — see ["The server packages"](RELEASING.md#the-server-packages) |
| `linux-service` ("Service lifecycle (Linux)") | `ubuntu-latest` | `scripts/ci/linux-service-test.sh binary`: `gwatch install`, then asserts the unit runs as `gwatch` with `/var/lib/gwatch` and only `CAP_NET_RAW`, passes `systemd-analyze verify` and scores at most 4.5 in `systemd-analyze security`, answers `/api/health`, takes a first administrator, a sign-in and a ping check with a result, and leaves nothing behind after `gwatch uninstall --purge --yes` |
| `agent-packaging` ("Agent packages") | `ubuntu-latest` | builds the agent's `.deb`/`.rpm` for all three architectures, installs the `.deb` on the runner's systemd and takes it through install → refuse to self-update → configure → run → purge, renders the Homebrew formula (`ruby -c`) and the winget manifests (validated against winget's 1.6.0 schemas), builds the agent image and has it read the runner through a mounted host root, and checks no package output is named like a self-update asset — see ["The agent packages"](RELEASING.md#the-agent-packages) |

Windows builds the Inno Setup installers and validates PowerShell scripts. Linux tests
the installed systemd service and journal output; Linux and macOS both exercise their
native collectors.

The `ci` job also compiles the winget variant of the agent setup program, so a change to
`scripts/installer/gwatch-agent.iss` that breaks it fails a pull request rather than a tag.

Releases are no longer started by pushing a `v*` or `agent-v*` tag. They are built by
[`.github/workflows/release.yml`](../../.github/workflows/release.yml), dispatched by hand
from `main` (**Actions → Release → Run workflow**, a dry run first — see the
[README](RELEASING.md#the-release-manifest-and-the-release-workflow) and
[`.release/README.md`](../../.release/README.md)). It re-runs the `ci`, `linux` and `macos`
jobs above, then builds, signs and publishes each app that `release.json` puts ahead of
its published version: `gwatch-build`/`gwatch-sign` for GWatch (the steps below), the
`gwatch-agent-*` and agent packaging jobs for the agent
(["The agent packages"](RELEASING.md#the-agent-packages)), and the container images only after the
release they belong to is published (["The container image"](RELEASING.md#the-container-image)).
Where the sections below tag a commit and push the tag, dispatch that workflow instead;
the tag it releases under comes from the version file. Pushing `mcp/v0.1.0` still runs `mcp-tag` in `ci.yml`, a guard
job — see ["Releasing the MCP companion"](RELEASING.md#releasing-the-mcp-companion).

## Repository layout

| Path | Purpose |
|---|---|
| `main.go` | CLI, service commands (through `internal/svc`), HTTP server bound to localhost by default |
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
| `internal/svc` | Background-service hosting for both programs: Windows Service Control Manager, launchd, systemd (OpenRC and System V as fallbacks) — see [the checklist below](RELEASING.md#service-hosting-manual-checks) |
| `internal/serviceinstall` | The protected copy of the program a service runs, and the server's own hardened systemd installation |
| `internal/mailer` | SMTP delivery and alert email rendering |
| `internal/backup` | Encrypted backup archives and restore |
| `internal/logging` | The size-rotated file + in-memory service log behind `/api/logs` |
| `internal/api` | JSON API (see [`API.md`](../API.md)) and static UI serving |
| `cmd/gwatch-agent` | The one-directional hardware agent installed on other machines ([`HARDWARE.md`](../HARDWARE.md)) |
| `cmd/gwatch-sign` | Maintainer CLI: generate the release signing key, sign and verify release assets |
| `cmd/gwatch-rsrc/` | Builds the `.syso` resource objects that put the icons inside the Windows executables — GWatch's mark in `gwatch.exe`, the agent's in `gwatch-agent.exe` (`make rsrc`) |
| `web/` | The browser interface (vanilla HTML/CSS/JS, no build step, embedded into the binary; open with `?mock=1` for an in-browser demo backend) |
| `web/fonts/` | Barlow and Kode Mono, latin subsets, self-hosted so the UI still requests nothing from the internet ([SIL OFL 1.1](../../web/fonts/OFL.txt)) |
| `tests/web/` | The jsdom suite for `web/` (`make web-test`). It lives outside `web/` so `//go:embed` never ships it |
| `tests/contract/` | `api-shapes.json`: the keys and JSON types of the real handlers' answers to a set of GETs, written by `internal/api/mock_contract_test.go` (`GWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes` after a handler changes) and held against `web/mock.js` by `tests/web/mock-contract.test.mjs`, so the demo backend cannot drift from the API unnoticed |
| `tsconfig.json` | Type checking for `web/` (`make web-typecheck`): JSDoc types, no build. Checked today: `components.js`, `api.js`, `fmt.js`, `chart-config.js`, `netmap.js` — a file opts in with `// @ts-check` as its first line; the steps are in `tsconfig.json`, and `tests/web/typecheck-coverage.test.mjs` keeps the list from shrinking |
| `tests/e2e/` | Playwright + axe over every route, in both themes (`make web-e2e`); `playwright.config.mjs` and `package.json` configure both suites |
| `skill/` | The downloadable agent skill served by `GET /api/mcp/skill`, versioned by `skill/VERSION` |
| `scripts/` | Windows build / install / uninstall PowerShell scripts |
| `scripts/installer/` | The two Inno Setup scripts, their shared branding and the wizard artwork |
| `packaging/` | The agent's packages: `build-agent.sh` (the packaged builds), `nfpm/` (`.deb`/`.rpm`, systemd unit, maintainer scripts), `homebrew/`, `winget/` and `docker/Dockerfile.agent` — see ["The agent packages"](RELEASING.md#the-agent-packages) |
| `VERSION` | The version every build reports; a release is the tag `v<VERSION>` |
| `mcp/` | The MCP companion, a separate Go module with its own `VERSION` — see [`mcp/README.md`](../../mcp/README.md) |
| `AGENTS.md`, `CLAUDE.md`, `development-docs/` | Governance for contributors and AI coding agents: identity, development standards and the Definition of Done, current architecture, the roadmap; `features.md`, `newFeatures.md`, `current-checklist/`, `audits/` and `human-qa/` are the working records that go with them, and `.claude/skills/` holds the two agent skills |
| `Dockerfile`, `.dockerignore`, `docker-compose.yml` | The container image (`make docker`) and a ready-to-run Compose file — see [`DOCKER.md`](../DOCKER.md) |
