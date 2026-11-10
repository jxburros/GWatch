# GWatch — Development Manifesto

> **For AI Agents and contributors:** this document defines how GWatch is built, validated, documented and maintained. Read `development-docs/coreIdentity.md` first; this file assumes it. Section 7 is the canonical Definition of Done.

<!--
Set up on 2026-11-10 from CONTRIBUTING.md, docs/development/DEVELOPMENT.md,
docs/development/RELEASING.md, .release/config.json, the Makefile and the CI workflows.
Items marked "Inferred — maintainer to confirm" are not stated outright in the repository.
-->

---

## 1. Purpose of This Manifesto

GWatch watches other people's networks and runs with real privileges on their machines (a Windows service as LocalSystem, a hardened systemd unit, a container with `NET_RAW`). It holds SMTP passwords, runs scripts and installs its own updates. That means a careless change can cost someone their network's security or their history. This manifesto sets the standard every change is held to.

---

## 2. Development Standard

A change to GWatch is good when it:

- Does one thing, and comes with a test that would have caught the problem it fixes.
- Keeps GWatch calm, local and safe by default (`coreIdentity.md` §3–§4).
- Keeps every screen working from the keyboard and with a screen reader.
- Leaves the document that describes the behaviour accurate.
- Passes `make ci`.

---

## 3. Core Development Principles

### 1. Safe by default, explicit when not

**The Principle:** Any behaviour that exposes GWatch, runs code, or sends data off the machine is off until a person turns it on, and is explained in plain language when they do.

**In practice:** loopback binding, sign-in for other devices, tokens generated for endpoints, the "this action can execute untrusted input" acknowledgement, read-only API keys by default, `--allow-write` for the MCP companion.

### 2. Evidence over assertion

**The Principle:** A behaviour is not done until a test shows it.

**In practice:** the Go suites run against local `httptest` servers and a fake DNS resolver (no external network); security fixes ship with regression tests (`*_regression_test.go`, `security_fixes_test.go`); the web mock is held to the real API by a contract test.

### 3. Narrow shapes

**The Principle:** When a feature brushes against enterprise territory, it ships in the narrowest shape that answers the real need, and that shape is written down as a constraint.

**In practice:** see `productRoadmap.md` §10 and `docs/development/AGENT-DECISIONS.md`. A change that widens one of those shapes is a product decision, not an implementation detail.

### 4. Comments say why

**The Principle:** Code says what; comments say why, including what was tried and rejected.

---

## 4. Project-Specific Development Doctrine

### Web interface (`web/`)

- **No build step and no runtime dependencies.** Plain HTML, CSS and JavaScript, embedded into the binary with `//go:embed`. `node_modules` is for tests only (`package.json` is test tooling and ships nothing).
- **Accessibility is a gate.** CI runs axe through Playwright over every route, in both themes. Every chart has "view as table".
- **The mock backend must match the API.** `web/mock.js` is checked against `tests/contract/api-shapes.json`; regenerate the contract with `GWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes` after a handler changes, and regenerate `web/mock-templates.js` when templates change.
- **Type checking only grows.** Files opt into `// @ts-check`; `tests/web/typecheck-coverage.test.mjs` keeps the list from shrinking.
- **No third-party resources.** Fonts and assets are self-hosted.

### JSON API and policy (`internal/api`)

- The JSON API is the contract the web interface, the MCP companion and integrators all use; document every change in `docs/API.md`.
- Authorisation is decided in one place, the route policy (`internal/api/policy.go`). Do not add a handler-level special case where a policy entry belongs.
- API keys never get access to settings, backups, logs, scripts, triggers, endpoints, rules, discovery, pairing, updates, users or keys.

### Storage (`internal/store`)

- All writes go through the single queued writer. Schema changes are migrations that work on SQLite, PostgreSQL and MySQL/MariaDB; SQLite is the default and is tested first.
- History is rolled up and pruned; a new kind of data must have a retention story.

### Secrets

- Secrets are sealed with `internal/secrets` before they are stored, masked in API responses, scrubbed from logs and errors, and never placed in examples, issues, release notes or changelog entries.

### Scripts and automation (`internal/actions`)

- Placeholder values that come from monitored devices or inbound requests are untrusted. They must be escaped or passed as data, never spliced raw into anything a shell or `git` interprets.

### The agent and the MCP companion

- `gwatch-agent` (`cmd/gwatch-agent`) and `gwatch-mcp` (`mcp/`, its own Go module) are separate programs with their own versions and releases. `gwatch-mcp` never imports GWatch's code; it uses the public API.
- Agent design decisions are recorded in `docs/development/AGENT-DECISIONS.md`; add an entry there for a new one.

### Writing

- Plain words and British spelling, in the interface and the documents.
- A new alert, banner or prompt has to earn its place.

---

## 5. AI Role in Development

AI agents may help with code, tests, documentation, debugging, reviews and issue investigation. They are not the final authority.

Sources of truth, in order: `coreIdentity.md`, `architecture.md`, `productRoadmap.md`, this manifesto, existing code behaviour, tests and other direct evidence, and the maintainers' decisions. When these conflict, identify the conflict and make the smallest safe change consistent with the request.

AI-assisted development must be evidence-based, small in scope unless asked otherwise, reversible where possible, documented and honest about uncertainty. It must not invent project direction, rewrite unrelated systems, hide uncertainty, treat generated code as correct without checking it, or make destructive changes (to releases, signing keys, tags, issues or branches) without explicit instruction.

Agents must never touch the release signing key, the `GWATCH_SIGNING_KEY` secret, `internal/update/release_keys.txt` or the release workflows except on an explicit maintainer request.

---

## 6. Change Discipline

Prefer small, focused changes, clear naming, explicit behaviour changes and documentation updates in the same change. Avoid broad rewrites, opportunistic refactors, silent behaviour changes and new dependencies (`go.mod` is deliberately short; `web/` has none).

For anything larger than a fix, an issue should exist and the approach should be agreed first (`CONTRIBUTING.md`).

When a request conflicts with the identity, this manifesto, the roadmap or the architecture, document the conflict rather than forcing the change through.

---

## 7. Canonical Definition of Done

A task is done only when all applicable items are true:

- The requested scope is implemented with the smallest safe change.
- The relevant context documents were consulted (`coreIdentity.md`, this manifesto, `architecture.md`, `productRoadmap.md` as applicable).
- A test exists that would have caught the problem, or the reason there is none is recorded.
- `make ci` passes locally (gofmt, `go vet`, `go mod tidy` check, race tests, the `mcp/` module, the jsdom web suite and the Playwright/axe suite). If part of it cannot run in the environment, say which part and why.
- Platform-specific changes (Windows service, installers, systemd, macOS collectors, PostgreSQL/MySQL) are noted as relying on CI or on the hand checks in `docs/development/RELEASE-TEST-PLAN.md`.
- `python3 scripts/ci/check-md-links.py .` passes when Markdown changed.
- The user or developer document that describes the changed behaviour is updated (`docs/README.md` is the index); `docs/API.md` for any API change.
- If anything under the release paths in `.release/config.json` changed, the affected app's version is advanced (patch by default) in `release.json` and its version file, release notes are added in `.release/notes/<app>/<version>.md`, and `CHANGELOG.md` has the entry — otherwise **Release version validation** fails. Ask the maintainer before choosing a minor or major bump.
- `README.md` is updated when installation, first-run, features or security notes change.
- `architecture.md` is updated when architecture changed; `productRoadmap.md` only for scope or direction changes.
- `features.md` is updated when a capability is added, removed or materially changed.
- Incomplete, blocked or skipped work, and handover notes, are recorded (see `AGENTS.md` §10).
- Security, privacy and data-handling expectations are preserved.

Other documents reference this section instead of keeping their own completion checklists.

---

## 8. Cross-Document Update Rules

Each document owns its own "when to update" rules:

- `README.md` owns README update triggers (`AGENTS.md` §11 summarises them).
- `development-docs/architecture.md` owns architecture update triggers (its §19).
- `development-docs/productRoadmap.md` owns roadmap update triggers (its §14).
- `CHANGELOG.md` and `AGENTS.md` §10 own changelog and handover rules.

---

## 9. Architecture Source of Truth

`development-docs/architecture.md` describes the system as it exists. Update it when a change adds or removes a subsystem, changes data flow, storage, authentication, external integrations, build/runtime/packaging, configuration precedence, or public contracts. Do not update it for localised fixes, copy changes or tests. Describe what exists, not what is planned.

---

## 10. Testing, Validation, and Evidence Expectations

| Command | What it runs |
|---|---|
| `make test` | `go vet` + Go unit and integration tests on SQLite |
| `make ci` | Everything CI runs locally (see §7) |
| `make test-postgres` / `make test-mysql` | Store, backup, API, engine and hostmon suites against a server (`docs/DATABASE.md`) |
| `make web-test` / `npm test` | jsdom suite in `tests/web/` |
| `make web-typecheck` / `npm run typecheck` | `tsc` over `// @ts-check` files |
| `make web-e2e` / `npm run test:e2e` | Playwright + axe over every route |
| `make mcp-test` | The `mcp/` module |
| `python3 scripts/ci/check-md-links.py .` | Relative links and anchors in Markdown |

CI (`.github/workflows/ci.yml`) additionally covers Windows (installers, PowerShell, Authenticode dry run), macOS collectors, PostgreSQL 16 and MySQL 8, the Docker image, server and agent packages, and the systemd service lifecycle. Do not claim those were exercised locally when they were not.

Never claim a test passed if it was not run.

---

## 11. Documentation Requirements

- User documents live in `docs/` and are indexed by `docs/README.md`; developer how-tos (build, test, release) live in `docs/development/`; governance lives in `development-docs/`.
- `CHANGELOG.md` is the user-facing record of each version; `.release/notes/` holds the release bodies.
- Every policy document (`docs/PRIVACY.md`, `docs/TERMS.md`, `docs/DISCLAIMER.md`, `SECURITY.md`) must stay true to the code.

---

## 12. Dependency and Configuration Philosophy

- Add a Go dependency only when the standard library and existing dependencies cannot reasonably do the job; say why in the change.
- `web/` gets no dependencies. Test tooling in `package.json` is pinned.
- Configuration precedence (flags, then environment, then files, then defaults) is documented in `architecture.md` §9; a new setting follows it.
- GitHub Actions are pinned by commit.

---

## 13. Error Handling and Failure Honesty

- Errors shown to people are in plain words, say what happened and what to do, and never contain secrets.
- GWatch reports its own health (Monitor health, the service log); a failure in GWatch must be visible, not silent.
- A failed signature, a failed migration or a failed restore stops; it never continues with a warning.

---

## 14. Agent Operating Rules

When working in this repo, AI agents must:

1. Read `development-docs/coreIdentity.md` before making changes.
2. Follow this manifesto.
3. Check `development-docs/productRoadmap.md` before implementing a new or directional feature, and check whether it is rejected or narrowly shaped.
4. Check `development-docs/architecture.md` before changing structure, dependencies, data flow, integrations, storage or module boundaries, and update it when architecture changes.
5. Make the smallest safe change; avoid unrelated refactors; preserve existing behaviour unless asked.
6. Validate with the commands in §10 and report what was and was not run.
7. Keep `README.md`, the relevant `docs/` page and `CHANGELOG.md` accurate.
8. Record incomplete work, uncertainty and skipped validation honestly.
9. Stop and ask when a request conflicts with the identity, safety rules, roadmap or architecture.
10. Never close, relabel or comment on GitHub issues, push tags, dispatch the Release workflow or change repository settings without an explicit request.

---

## 15. Final Principle

GWatch should be developed so that the people who install it can trust it with their network. Every change should leave it easier to understand, safer to run, more aligned with its identity and easier for humans and AI agents to maintain responsibly.
