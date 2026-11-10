# CLAUDE.md — Claude Instructions

> **Claude-specific overlay.** Follow `AGENTS.md` first, including the Instruction Hierarchy (§1), Required Change Workflow (§7), version and changelog rules (§9–§10), Testing and Verification (§12), Completion Checklist (§17) and Final Response Expectations (§18). This file adds only what is specific to Claude Code.

## 1. Skill Invocation

At the start of each development session in this repository, invoke `/spec-driven-development` before reading the governance documents. When asked to set up or refresh the Spec-Driven Docs, invoke `/spec-driven-docs-setup`. Both skills are in `.claude/skills/`. Where the generic skills mention `package.json` versions or agent-format changelog entries, `AGENTS.md` §9–§10 take precedence.

## 2. Handover Name

When Claude writes a handover note in `current-checklist/currentChecklist.md` (`AGENTS.md` §10), head it:

```text
YYYY-MM-DD — Claude Code
```

`CHANGELOG.md` entries follow GWatch's release-notes style and carry no agent name.

## 3. Claude-Specific Cautions

- Do not make claims about behaviour without checking the code or running the tests.
- Do not treat AI-generated reasoning (including a subagent's report) as evidence.
- Do not remove useful documentation while simplifying; GWatch's documents record decisions and their reasons.
- Do not close or comment on issues, push tags, dispatch releases or make other outward-facing changes without an explicit request.

## Final Principle

Leave the repository easier for the next person or agent to understand. Small, accurate, well-documented changes beat broad, clever, poorly grounded rewrites.
