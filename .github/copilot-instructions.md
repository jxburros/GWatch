# GitHub Copilot Instructions

> **Copilot-specific overlay.** Follow [`AGENTS.md`](../AGENTS.md) first, including its Instruction Hierarchy (§1), version and changelog rules (§9–§10) and Definition of Done (`development-docs/developmentManifesto.md` §7). This file adds only what is specific to Copilot.

- Before reading the governance documents, read `.claude/skills/spec-driven-development/SKILL.md`. For setting up or refreshing the docs, read `.claude/skills/spec-driven-docs-setup/SKILL.md`. `AGENTS.md` §9–§10 take precedence over those skills' generic `package.json` and changelog-format guidance.
- Make surgical changes; do not refactor unrelated code.
- Keep `web/` free of dependencies and build steps, and keep every screen keyboard- and screen-reader-accessible.
- If a change is incomplete or conflicts with documented direction, say what was not done and why, in the pull request description or the handover notes in `current-checklist/currentChecklist.md`.
