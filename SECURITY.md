# Security policy

## Reporting a vulnerability

Please report security problems privately, not in a public issue.

Use GitHub's private reporting: on the repository's
[Security tab](https://github.com/jxburros/GWatch/security), choose **Report a
vulnerability**. Only the maintainers can read what you send.

Include what you can of:

- the version (**Settings › Updates**) and how GWatch is installed;
- what an attacker needs to start with (network access, a viewer account, an API
  key and its scope);
- the steps to reproduce it, and what it lets them do.

You will get an acknowledgement, and we will tell you what we find and when a fix
is released. Please give us the chance to release a fix before you publish details.

## Which versions get fixes

GWatch is in beta. Security fixes go into the next release, and only the newest
release is supported: update to it. **Settings › Updates** installs it for you on
Windows and on a standalone Linux or macOS install; packages and containers are
updated the way they were installed.

## What GWatch treats as its security boundary

So that a report lands on something we would fix:

- **A person at the computer GWatch runs on is an administrator by default,** with
  no sign-in. This is deliberate, documented in
  [`docs/DISCLAIMER.md`](docs/DISCLAIMER.md#how-gwatch-ships), and can be turned
  off with **Require sign-in on this computer too**. A local user getting
  administrator access while that setting is off is not a vulnerability. A *remote*
  client, a web page, a viewer account or an API key getting it is.
- **Administrators can run code** on the host through custom checks and script
  actions. That is a feature. A viewer or an API key being able to is a
  vulnerability.
- **GWatch is not meant to face the internet directly.**
  [`docs/REMOTE-ACCESS.md`](docs/REMOTE-ACCESS.md) describes the supported ways to
  reach it from outside.
- Secrets are encrypted at rest with a key stored beside the database. This
  protects copies and dumps of the database, not against someone who can already
  read the data directory.

## Verifying a download

Every release file is signed with GWatch's release key, and GWatch checks that
signature before it installs an update. The Windows setup programs are not yet
signed with a Windows (Authenticode) certificate, so Windows warns about an unknown
publisher. [`docs/INSTALL.md`](docs/INSTALL.md#known-limitations) says more about that
warning.
