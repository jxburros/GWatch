# AGENTS.md — General Agent Instructions

> **Canonical AI-agent instruction source for GWatch.**
>
> This is the primary instruction document for AI agents, coding assistants and automation tools working in this repository. Tool-specific files (`CLAUDE.md`, `.github/copilot-instructions.md`) defer to this file and reference the Instruction Hierarchy in Section 1 instead of redefining it.

---

## 1. Instruction Hierarchy

1. User request
2. `development-docs/coreIdentity.md`
3. `development-docs/design.json` for UI and styling work, when it contains a generated design contract (today it is a placeholder)
4. `development-docs/developmentManifesto.md`
5. `development-docs/architecture.md`
6. Relevant plans in `development-docs/architecture-plans/` for structural decisions, when present
7. `development-docs/productRoadmap.md`
8. `AGENTS.md`
9. Tool-specific files such as `CLAUDE.md` or `.github/copilot-instructions.md`
10. Existing code, tests and documentation patterns (including `CONTRIBUTING.md`, `docs/development/` and `docs/development/AGENT-DECISIONS.md`)

If these sources conflict, do not silently choose one. Identify the conflict, make the smallest safe decision available, and document it.

---

## 2. Required Documentation Access

- Read `development-docs/coreIdentity.md` before making code, architecture, product, UX, workflow or documentation changes.
- Consult `development-docs/developmentManifesto.md` when changing behaviour, quality standards, testing, automation or contributor workflow.
- Consult `development-docs/architecture.md` before changing structure, module boundaries, data flow, storage, authentication, build/runtime/packaging, external integrations or public contracts.
- Consult `development-docs/design.json` before UI work only once it contains a generated contract. The placeholder is not a contract and agents must not fill it in; until then follow `web/app.css`, `brand/` and existing `web/` patterns.
- Check `development-docs/architecture-plans/` before structural decisions. If no plan exists and the decision needs one, ask before creating it.
- Consult `development-docs/productRoadmap.md` before implementing a new capability or changing scope.

If a referenced document is missing, proceed with the available context and record it in the handover notes (Section 10).

---

## 3. Document Decision Tree

| Task Type | Required Context |
|---|---|
| Any meaningful change | `development-docs/coreIdentity.md` |
| New feature or user-facing capability | `development-docs/productRoadmap.md` (stage, §10 rejected directions and narrow shapes) |
| Refactor, restructuring or dependency change | `development-docs/architecture.md` |
| Agent (`cmd/gwatch-agent`) behaviour | `docs/development/AGENT-DECISIONS.md` |
| UI or styling work | generated `design.json` when present; `web/` conventions; accessibility rules in the manifesto |
| Security, auth, policy, secrets | `architecture.md` §12, `SECURITY.md`, `docs/PRIVACY.md` |
| Build, test, release, packaging | `docs/development/DEVELOPMENT.md`, `docs/development/RELEASING.md` |
| AI behaviour, automation, testing or process | `development-docs/developmentManifesto.md` |
| Setup, usage, commands or public behaviour | `README.md` and the page `docs/README.md` points to |
| Meaningful completed work | `CHANGELOG.md` (Section 10) |

---

## 4. Document Mission Table

| Document | Agent Mission |
|---|---|
| `development-docs/coreIdentity.md` | **Guardrails:** what GWatch is, protects, serves and must not become |
| `development-docs/developmentManifesto.md` | **Standards:** how GWatch is built and validated; canonical Definition of Done (§7) |
| `development-docs/architecture.md` | **Navigation and truth:** the current structure and invariants |
| `development-docs/design.json` | **Visual contract:** placeholder until generated |
| `development-docs/architecture-plans/` | **Decision guidance:** feature plans; `archives/` holds shipped ones |
| `development-docs/research/` | **Research:** active curated research |
| `development-docs/productRoadmap.md` | **Direction:** stages toward 1.0, deferred and rejected directions |
| `features.md` | **Capability inventory:** what is believed to exist, tested or not |
| `newFeatures.md` | **Historical intake:** ideas not yet tracked by an issue |
| `current-checklist/currentChecklist.md` | **Working context:** the current period, plus agent handover notes |
| `audits/`, `human-qa/` | Automated audit reports and human QA reports; `archives/` once processed |
| `README.md` | **Public entry point** for people installing GWatch |
| `CHANGELOG.md` | **Release notes**, version by version |
| `CLAUDE.md`, `.github/copilot-instructions.md` | Tool overlays |

---

## 5. Project Summary

GWatch is a self-hosted, local-first network and service monitor for a home or small office. It is one Go binary with an embedded, dependency-free web interface, plus two separately versioned companions: `gwatch-agent` (hardware readings and remote collectors) and `gwatch-mcp` (an opt-in MCP server). It is in beta (0.8.11); 1.0 is reserved for the first public, stable release.

Important context:

- `docs/` is user documentation (indexed by `docs/README.md`); `docs/development/` is developer how-to (build, test, release, agent decisions); `development-docs/` is governance. Do not confuse the last two.
- `CHANGELOG.md` is user-facing release notes, not an agent log (Section 10).
- Versions come from `release.json` and the version files, not `package.json`, which is test tooling only (Section 9).
- The GitHub issues are the task list; the roadmap points to them.

---

## 6. Non-Negotiable Project Values

Agents must preserve these (details in `coreIdentity.md` §12):

- **Local-first, no telemetry:** nothing leaves the machine unless the person configured it.
- **Safe by default:** loopback binding, sign-in for other devices, sealed secrets, signed updates with no silent skip, no scripts from API keys.
- **Calm by default:** a new alert, banner or prompt has to earn its place.
- **Accessible:** every screen works from the keyboard and with a screen reader.
- **No build step or dependencies in `web/`.**
- **Narrow shapes hold:** SNMP, the agent, remote collectors and discovery stay in the shapes recorded in `productRoadmap.md` §10.
- **No AI in the core.**
- **Capability traceability:** keep `features.md` current; listing a capability does not claim it was tested.

If a requested change conflicts with these values, identify the conflict before implementing.

---

## 7. Required Change Workflow

1. Align with context: read the required documents for the task type.
2. Identify the governing principles and constraints.
3. Inspect the relevant files and tests. No blind changes.
4. Make the smallest safe change; preserve behaviour unless the request changes it.
5. Avoid unrelated refactors.
6. Validate (Section 12).
7. Update the documentation that describes the behaviour.
8. Update `CHANGELOG.md` and the release metadata when required (Sections 9–10).
9. Report honestly.

With several requested tasks, complete as many as feasible; if one is blocked, continue with the others and record what was not done.

---

## 8. Architecture Source-of-Truth Rules

`development-docs/architecture.md` describes the current architecture. Consult it before changing structure, module boundaries, data flow, storage or migrations, authentication or the route policy, the trust model, external integrations, build/packaging/release, configuration precedence, the API, the CLI or the agent protocol. Update it carefully when those change (its §19 owns the triggers). Do not turn it into a roadmap, changelog or file inventory.

---

## 9. Product Roadmap Rules

`development-docs/productRoadmap.md` is version-level direction. It is not a sprint board or task tracker. Do not update it for ordinary progress; its §14 owns the triggers.

### Version and Pull Request Rules

- **`release.json` is the authoritative release manifest.** Keep `VERSION` (GWatch), `cmd/gwatch-agent/VERSION`, `mcp/VERSION` and `skill/VERSION` in step with it as `docs/development/RELEASING.md` describes. `package.json` is test tooling and carries no product version.
- **Every pull request into `main` must advance at least one app's version**, documentation-only changes included: `.release/config.json` sets `bumpPolicy: every-main-change`, and **Release version validation** fails otherwise ("Every main change requires a version advance"). The per-app `paths` there say which apps a change *affects*: advance each affected app (patch by default); when no app's paths are touched, advance GWatch, as 0.8.9 and 0.8.11 did. Each advance needs the version file, `release.json`, `.release/notes/<app>/<version>.md`, a `CHANGELOG.md` entry and, for GWatch, the beta version line in `README.md`.
- Before opening a pull request, ask the maintainer whether the change should bump a version, and if so whether patch, minor or major. Do not choose minor or major yourself.
- Never dispatch the Release workflow, push tags, or touch signing keys, secrets or `internal/update/release_keys.txt` without an explicit request.

---

## 10. Changelog Rules

GWatch's `CHANGELOG.md` has an established convention, which takes precedence over the generic Spec-Driven Docs agent-entry format:

- Entries are per version (`## 0.8.11 (not yet released)`), grouped under headings such as `### Security`, `### Fixed`, `### Documentation`, and written for the people who run GWatch: what changed for them and why, in plain words. Look at the existing entries for style.
- Add to the newest unreleased version's entry. If the change requires a version bump (Section 9), the entry belongs to the new version.
- Never rewrite published entries; the file is append-only in spirit.
- Never put secrets, tokens or private data in an entry.

**Agent handover notes** (incomplete work, open questions, risks for the next agent) do not go in `CHANGELOG.md`. Put them under "Handover notes" in `current-checklist/currentChecklist.md`, headed `YYYY-MM-DD — <Tool Name>`, for example `2026-11-10 — Claude Code`. Remove a note when it no longer applies. A pull request description is the other place for them.

If everything requested was completed, the handover note can say so in one line, or be omitted.

---

## 11. README Rules

`README.md` is written for the person installing GWatch. Update it when installation, the first ten minutes, the feature summary, the comparison table, the security notes or the "What's new" summary change. Developer detail belongs in `CONTRIBUTING.md` and `docs/development/`.

---

## 12. Testing and Verification

Run what applies (full list in `developmentManifesto.md` §10):

```bash
make test        # go vet + Go tests on SQLite
make ci          # everything CI runs locally, including the web suites
npm test         # jsdom web suite
npm run test:e2e # Playwright + axe
python3 scripts/ci/check-md-links.py .   # Markdown links and anchors
```

Windows installers, macOS collectors, PostgreSQL/MySQL, packaging and the service lifecycle are exercised in CI and by the hand checks in `docs/development/RELEASE-TEST-PLAN.md`. Do not claim something was tested if it was not.

---

## 13. Safety, Privacy, and Data Rules

Do not introduce default behaviour that deletes user data, sends data off the machine, exposes GWatch beyond loopback, weakens authentication or the route policy, skips signature verification, runs untrusted input in a shell, suppresses important logs, or leaks secrets. Any mutating, external or exposing behaviour must be explicit, documented, opt-in, scoped and safe by default.

Secrets must not appear in logs, reports, exports, API responses, error messages, issue bodies, changelog entries or examples. Security vulnerabilities are reported privately (`SECURITY.md`), never in a public issue.

Do not close, relabel or comment on issues, push to other branches, or change repository settings without an explicit request.

---

## 14. AI Behaviour Rules

GWatch has no AI features. When working on the MCP companion or the agent skill:

- Keep monitoring content labelled as untrusted data; never let it become instructions.
- Keep write tools behind both a read-write key and `--allow-write`; GWatch's route policy remains the real boundary.
- Keep the companion on the public API; never import GWatch's internal packages into `mcp/`.
- Keep AI optional: no monitoring, alerting or set-up path may depend on it.

---

## 15. Domain-Specific Rules

### Skill Usage

When working in this repository, invoke the `spec-driven-development` skill before reading the development documents. When asked to set up or update the Spec-Driven Docs, invoke the `spec-driven-docs-setup` skill. Both are installed in `.claude/skills/`; agents without skill support read `.claude/skills/spec-driven-development/SKILL.md` and `.claude/skills/spec-driven-docs-setup/SKILL.md` directly. Where those generic skills mention `package.json` versions or agent-format changelog entries, Sections 9 and 10 of this file take precedence.

### Operational Records

- `features.md` is an inventory of capabilities believed to be implemented; update it when coverage changes.
- `newFeatures.md` keeps intake history after promotion.
- `current-checklist/currentChecklist.md` is an organisational aid and holds handover notes; it does not limit work.
- `audits/` holds automated audits and `human-qa/` human QA. Earlier full reviews live in `docs/development/` (`REVIEW-2026-10-31.md`); new ones go in `audits/`. Fully processed reports may move into `archives/`.
- `development-docs/research/` is active research; ask before creating new research or architecture-plan documents.

### Writing Style

Plain words, British spelling, comments that say why. Match the tone of the existing documents.

---

## 16. Code Style

Prefer clear, small functions, explicit parameters, helpful plain-language errors, defensive parsing and the existing conventions (`gofmt`, `go vet`, staticcheck; vanilla JavaScript with JSDoc types in `web/`). Avoid unrelated rewrites, silent failures, new dependencies, duplicated logic and abstractions ahead of need.

---

## 17. Completion Checklist

Before finishing, confirm the Definition of Done in `development-docs/developmentManifesto.md` §7 is met, in particular:

- [ ] `coreIdentity.md`, and the manifesto, architecture and roadmap where relevant, were considered.
- [ ] The change is the smallest safe one and preserves existing behaviour unless asked.
- [ ] Validation was run, or skipped validation was recorded.
- [ ] Docs describing the behaviour, `README.md`, `architecture.md` and `features.md` were updated where affected.
- [ ] `CHANGELOG.md` and the release metadata were updated where required; the maintainer was asked about the version bump before a pull request.
- [ ] Incomplete work and handover notes were recorded.

---

## 18. Final Response Expectations

When reporting completion, summarise what changed and in which files, which documents were consulted and updated, what validation was run, what could not be completed, and any useful next step. Do not hide limitations or claim validation that was not performed.

---

## 19. Custom Repo Instructions

### Important Commands

```bash
go run . run --data-dir ./data     # run from source at http://127.0.0.1:7230 (/?mock=1 for the demo backend)
make test                          # go vet + tests
make ci                            # full local CI
npm install && npm test            # web test tooling and jsdom suite
npm run test:e2e                   # Playwright + axe
make mcp-test                      # the mcp/ module
python3 scripts/ci/check-md-links.py .
GWATCH_UPDATE_CONTRACT=1 go test ./internal/api -run TestMockContractShapes   # after an API handler changes
```

### Important Paths

| Path | Purpose |
|---|---|
| `main.go` | CLI and server wiring |
| `internal/api/policy.go` | The route policy: who may call what |
| `internal/engine`, `internal/checks`, `internal/store` | Engine, check runners, persistence |
| `web/`, `web/views/` | The interface (no build step) |
| `cmd/gwatch-agent` | The agent, own `VERSION` |
| `mcp/` | The MCP companion, own module and `VERSION` |
| `release.json`, `.release/` | Release manifest, path map and notes |
| `docs/development/` | Developer how-tos, release process, agent decisions, reviews |
| `.claude/skills/` | The Spec-Driven Docs skills |

### Known Gotchas

- `.gitignore` ignores `.claude/*` except `.claude/skills/`; local Claude state stays out of git.
- `scripts/ci/check-md-links.py` runs in CI and checks every relative link and anchor in Markdown (it skips `.claude/` and `.release/notes/`).
- `tests/web/encoding.test.mjs` fails on mojibake in `web/`, `docs/` and root Markdown files.
- `web/mock.js` must match the real API (`tests/contract/api-shapes.json`); regenerate the contract and `web/mock-templates.js` rather than hand-editing.
- The engine and store have platform- and dialect-specific code exercised only in CI (Windows, macOS, PostgreSQL, MySQL).
- Most of the October and November review findings are open issues; check the issue before "fixing" something the review describes, since the issue may record a decision.

### Maintainer Preferences

- Open an issue before anything larger than a fix (`CONTRIBUTING.md`).
- One subject per change, with a test that would have caught the problem.

---

## Final Principle

Leave GWatch better than you found it: aligned with its identity, safe to run, honest about uncertainty, documented where needed, consistent with the current architecture and the current stage of the roadmap.
