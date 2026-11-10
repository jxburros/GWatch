---
name: spec-driven-development
description: Develop in a repository governed by the Spec-Driven Docs system (AGENTS.md plus development-docs/coreIdentity.md, design.json, developmentManifesto.md, architecture.md, architecture-plans, and productRoadmap.md). Use when making code, documentation, configuration, or workflow changes in a repo that contains these files. Teaches which documents to read per task type, the instruction hierarchy, the required change workflow, changelog format, feature inventory, version check, and Definition of Done.
---

# Spec-Driven Development

You are working in a repository that uses the Spec-Driven Docs governance system. The repo's documents — not your assumptions — define what the project is, how it is built, and what "done" means. Read what is written, follow it, and do not invent what isn't there.

## Step 0: Confirm the system is present

Check for these files at the repo root:

```
AGENTS.md
CLAUDE.md (or another tool-specific overlay for your agent)
CHANGELOG.md
development-docs/coreIdentity.md
development-docs/design.json (for UI work; may be a placeholder)
development-docs/developmentManifesto.md
development-docs/architecture.md
development-docs/architecture-plans/ (when present)
development-docs/productRoadmap.md
features.md
newFeatures.md
current-checklist/currentChecklist.md (if present)
```

- If a document is **missing**, proceed with available context and record the missing file in your `CHANGELOG.md` entry.
- If a document still contains **template placeholders** (`[Project Name]`, `[Rule 1]`, etc.), work only from the filled-in parts. Do not treat placeholder text as instructions, and do not invent content for unfilled sections. Note significantly incomplete docs in your changelog entry, and suggest the maintainer run the `spec-driven-docs-setup` skill.

## Instruction hierarchy

When sources conflict, follow this precedence (highest first):

1. The user's explicit request in the current session
2. `development-docs/coreIdentity.md` — identity and non-negotiable constraints
3. `development-docs/design.json` — generated styling contract for UI work; ignore the placeholder
4. `development-docs/developmentManifesto.md` — development standards, agent rules, Definition of Done
5. `development-docs/architecture.md` — current structure and architectural invariants
6. Relevant `development-docs/architecture-plans/` plans — feature-specific structural guidance
7. `development-docs/productRoadmap.md` — version scope and product direction
8. `AGENTS.md` — general agent workflow
9. Tool-specific files (`CLAUDE.md`, `.github/copilot-instructions.md`)
10. Existing code, tests, and documentation patterns

Never silently violate a higher-priority document to satisfy a lower one. If sources conflict, identify the conflict, make the smallest safe decision, and document it. If the *user's request* conflicts with a hard constraint, "Never" rule, rejected roadmap item, or architectural invariant, surface the conflict and ask before implementing — explain what you found so the user can decide to override it.

## What to read, per task

Read only what the task requires — over-reading wastes context on small tasks.

| Task type | Read before starting |
|---|---|
| Any meaningful change | `AGENTS.md` Section 1 (hierarchy) + `coreIdentity.md` (especially "What This Project Is Not" and "Hard Constraints / Never List") |
| Changing code behavior | + `developmentManifesto.md` (principles, Agent Operating Rules, Definition of Done) |
| Changing structure, modules, data flow, storage, integrations, dependencies | + `architecture.md` (especially Architectural Invariants and the directory map) |
| UI or styling work | + generated `development-docs/design.json`; never invent the placeholder contract |
| Structural decision needing feature-specific guidance | Check `development-docs/architecture-plans/`; ask before creating a missing plan |
| New feature or user-facing capability | + `productRoadmap.md` (is it in current-version scope? is it listed as Rejected?) |
| Updating feature coverage | Update `features.md` automatically; it is not proof of testing |
| AI behavior, automation, testing, or safety changes | + `developmentManifesto.md` safety and AI-role sections |
| Anything touching secrets, user data, or destructive operations | + `developmentManifesto.md` Safety section — these rules apply regardless of how the task is framed |
| Your own agent's behavior | Your tool-specific overlay file, after `AGENTS.md` |

Key interpretation rules:

- `coreIdentity.md` describes **fixed properties**, not aspirations. Its constraints are absolute; do not implement workarounds that technically comply but violate the spirit.
- `architecture.md` describes **current state**. If it contradicts the code, flag the discrepancy instead of guessing which is right. If your change alters architecture, updating this document is part of the change, not a separate task.
- `productRoadmap.md` describes **direction, not commitment**. "Planned" items are not specs, "Future Candidate" items are not approved work, and "Rejected" items must not be implemented without explicitly flagging the rejection to the user.

Operational records have narrower roles:

- `features.md` is a granular checklist of capabilities believed to be implemented, whether tested or not. Update it automatically when feature coverage changes.
- `newFeatures.md` is historical intake. Retain entries after promoting them to the roadmap or current checklist.
- `current-checklist/currentChecklist.md` is organizational context for the current sprint or working period, not a scope gate.
- `audits/` and `human-qa/` hold automated and human QA records. A fully processed report may move into its matching `archives/` folder.
- `development-docs/research/` remains active and is not automatically archived.
- Ask before creating a new research or architecture-plan document. A completed and tested architecture plan may move into `development-docs/architecture-plans/archives/`.

## Change workflow

Before editing:

1. Identify which documents govern the request (table above) and read the relevant sections.
2. Identify the smallest safe change that satisfies the request.
3. Check the change against invariants, hard constraints, and "never" rules.
4. If the request conflicts with a document, state the conflict before implementing.

During implementation:

- Preserve existing behavior unless the request explicitly changes it.
- Follow existing conventions; prefer clear, boring, maintainable code.
- No scope creep: no unrelated refactors, no speculative architecture, no "while I'm in here" additions.
- Document uncertainty instead of hiding it.
- If one of several requested tasks is blocked, continue with the others and record the blocker.

After implementation:

- Run relevant validation (tests, type checks, lint, build, manual verification). Look for commands in `AGENTS.md` "Important Commands", the tool-specific file, `README.md`, or `package.json`/build files.
- **Never claim validation ran when it didn't.** If validation could not run, say so and record it in the changelog entry.
- Update the documents the change affects (see next section).
- Verify the Definition of Done in `developmentManifesto.md` and the Completion Checklist in `AGENTS.md`.
- Update `features.md` when feature coverage changed.
- Before opening a pull request, ask whether `package.json` should receive a patch, minor, or major version bump. Do not open the PR until that decision is clear.

## Which documents to update after a change

| Document | Update when | Do NOT update for |
|---|---|---|
| `CHANGELOG.md` | Every meaningful session: completed work, incomplete/blocked work, skipped validation, intentionally-not-made changes | Nothing exempt — always append an entry for meaningful work |
| `README.md` | Setup, usage, commands, configuration, features, user-facing behavior, required secrets/services, or known limitations change | Purely internal refactors |
| `development-docs/architecture.md` | Module boundaries, data flow, storage, integrations, security boundaries, build/runtime/deployment, generated artifact formats, or config schema change | Small code edits, bug fixes inside a module, typos, tests |
| `development-docs/productRoadmap.md` | Version scope or product direction actually changes | Ordinary implementation progress — this is the most common mistake |

When updating `architecture.md`: describe what now exists (never speculative future state), keep accurate content, remove stale content, and don't turn it into a changelog or file inventory. If unsure how to update it, add a small conservative note or flag the uncertainty rather than inventing detail.

## Changelog entry format

Append to `CHANGELOG.md` (never delete, reorder, or rewrite prior entries). Use the identity format `Agent: <Tool Name> (<Model Name>)` — check your tool-specific file for the exact name to use.

```markdown
## YYYY-MM-DD — Agent: <Tool Name> (<Model Name>)

### Changed
- [What changed and why, not just what]

### Not completed
- [Anything incomplete, blocked, or skipped — or "None." only if truly everything was done]

### Notes
- [Validation run or not run, and why]
- [Assumptions made; missing or placeholder docs encountered]

### Handover
- Next agent should start with: ...
- Open questions: ...
- Risks or assumptions: ...
```

## When to stop and ask the user

- The request violates a Hard Constraint / "Never" rule in `coreIdentity.md`
- The request implements something listed as Rejected / Out-of-Scope in `productRoadmap.md`
- The change would break an Architectural Invariant in `architecture.md`
- The task is ambiguous between approaches with different architectural implications
- A feature needs a new architecture plan or curated research before implementation
- You are about to make a destructive or hard-to-reverse change that was not explicitly requested

## Reporting completion

Summarize: what changed, which files changed, whether `CHANGELOG.md` / `README.md` / `architecture.md` were updated, which `development-docs/` files were consulted, what validation was run (or honestly could not be run), what was not completed, and a useful next step. Do not hide limitations.
