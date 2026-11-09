# Developer documentation

These documents are for people building, changing or releasing GWatch. If you are
installing or using it, start at the [documentation index](../README.md) instead.

| Document | What it covers |
|---|---|
| [`DEVELOPMENT.md`](DEVELOPMENT.md) | Running from source, the `make` targets, the test suites, continuous integration, and where everything lives in the repository |
| [`RELEASING.md`](RELEASING.md) | The release manifest and workflow, signing, the installers and packages, the container image, the agent's and the MCP companion's own releases |
| [`RELEASE-TEST-PLAN.md`](RELEASE-TEST-PLAN.md) | The checks done by hand before a release |
| [`AGENT-DECISIONS.md`](AGENT-DECISIONS.md) | Why the agent is built the way it is: each decision, what was rejected, and when to revisit it |
| [`PRODUCT-BRIEF.md`](PRODUCT-BRIEF.md) | The original product brief. Kept as history: several of its non-goals have since shipped in a narrow form, and [`ROADMAP.md`](../../ROADMAP.md) holds the current scope |
| [`REVIEW-2026-10-31.md`](REVIEW-2026-10-31.md) | A full review of GWatch 0.8.5. Its findings are tracked as issues |

Elsewhere in the repository:

- [`CONTRIBUTING.md`](../../CONTRIBUTING.md): how to report a bug or propose a change.
- [`SECURITY.md`](../../SECURITY.md): how to report a vulnerability.
- [`ROADMAP.md`](../../ROADMAP.md): what is planned, and what GWatch will deliberately not become.
- [`docs/API.md`](../API.md): the JSON API, which is also the contract the web interface uses.
- [`docs/DATABASE.md`](../DATABASE.md#for-developers): running the tests against PostgreSQL or MySQL.
- [`.release/README.md`](../../.release/README.md): the release toolkit's inventory for this repository.
- [`brand/README.md`](../../brand/README.md) and [`scripts/installer/README.md`](../../scripts/installer/README.md): logos and installer artwork.
