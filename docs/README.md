# GWatch documentation

Find the document by what you are trying to do. New here? Read the
[README](../README.md) for what GWatch is and how to install it, then the
[user guide](USER-GUIDE.md).

## Install it

| I have | Read |
|---|---|
| Windows | [`INSTALL.md`](INSTALL.md): the setup program, upgrading, uninstalling, where the data lives |
| Linux (including a Raspberry Pi) | [`INSTALL-LINUX.md`](INSTALL-LINUX.md): the `.deb` and `.rpm` packages, the signed installer, the systemd service |
| Docker | [`DOCKER.md`](DOCKER.md): the first account, the `/data` volume, what changes inside a container |
| macOS | The [README](../README.md#install) has the two commands; the service behaves as on Linux |

## Use it

| I want to | Read |
|---|---|
| Learn the interface, screen by screen | [`USER-GUIDE.md`](USER-GUIDE.md) |
| Fix something that is not working | [`USER-GUIDE.md` › When something is wrong](USER-GUIDE.md#20-when-something-is-wrong) |
| Make GWatch do something when a check changes | [`RECIPES.md`](RECIPES.md): ready-made triggers and endpoints for Home Assistant, chat, push, Docker and git |
| Back up, restore, or move to a new machine | [`RESTORE.md`](RESTORE.md) |
| Reach GWatch from outside my network | [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) |
| Keep the data on PostgreSQL or MySQL | [`DATABASE.md`](DATABASE.md) |

## Watch more things

| I want to watch | Read |
|---|---|
| Another computer's processor, memory, disks, containers | [`HARDWARE.md`](HARDWARE.md): the agent, pairing, what each system can measure |
| Devices at a second site | [`COLLECTORS.md`](COLLECTORS.md) |
| A router, switch or access point | [`SNMP.md`](SNMP.md) |
| Smart-home devices | [`MATTER.md`](MATTER.md) |

## Connect it to other software

| I want to | Read |
|---|---|
| Script against GWatch | [`API.md`](API.md): authentication, roles and keys, every endpoint |
| Let an AI assistant read my monitoring | [`../mcp/README.md`](../mcp/README.md), and **Settings › AI & MCP** in the app |

## Know where you stand

| | |
|---|---|
| [`PRIVACY.md`](PRIVACY.md) | What GWatch stores, where, and that none of it leaves your machine |
| [`TERMS.md`](TERMS.md) | Terms of use |
| [`DISCLAIMER.md`](DISCLAIMER.md) | The security choices that are yours to make |
| [`../SECURITY.md`](../SECURITY.md) | Reporting a vulnerability |
| [`../LICENSE`](../LICENSE) and [`../TRADEMARKS.md`](../TRADEMARKS.md) | The licence, and the policy on the name and logo |
| [`../CHANGELOG.md`](../CHANGELOG.md) | What changed in each version |
| [`../development-docs/productRoadmap.md`](../development-docs/productRoadmap.md) | What is planned and what is known to be missing |

## Work on GWatch itself

[`development/`](development/README.md) holds the developer documents: building and
testing, the release process, the agent's design decisions and the original product
brief. Start with [`../CONTRIBUTING.md`](../CONTRIBUTING.md).
