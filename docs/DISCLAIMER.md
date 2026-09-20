# Security and liability disclaimer

GWatch hands you a lot of rope. This page is about which of those choices are
yours to make, what each one costs when it goes wrong, and where our
responsibility ends — which is at the point where you make the choice.

Nothing here is a secret. The same facts are in
[`INSTALL.md`](INSTALL.md), [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) and the
README; they are collected here so that "I did not know" is not available to
either of us.

## How GWatch ships

**There is no password by default.** A fresh install is usable immediately: a
browser on the computer GWatch runs on is treated as an administrator without
signing in. That is deliberate — the default install listens on `127.0.0.1`,
where the only thing that can reach it is something already running on that
machine, and demanding a password to protect yourself from yourself is theatre.

**It speaks plain HTTP.** GWatch terminates no TLS of its own. On loopback that
does not matter; anywhere else it means passwords, session cookies and API keys
cross the wire in the clear unless you put a reverse proxy with TLS in front.

**Binding to `0.0.0.0` exposes all of it.** Turning on Settings › Network
access, checking the installer's "Allow other devices on my network" box, or
passing `--listen 0.0.0.0:7230` makes every device on your LAN able to reach
GWatch. If you have not created an account by then, every device on your LAN is
an administrator: it can read your entire network map, change checks, read your
audit log, create a trigger that runs arbitrary commands on that machine, and
restore a database from a file it uploads. The installer warns about this on the
Network Access page. Please believe it.

**Automation runs as the service.** Triggers, custom endpoints and Custom script
checks execute commands on the GWatch machine with the service's permissions —
on Windows, that is a service account with real reach. Administrator access to
GWatch is, in practice, code execution on that computer.

## Choices that are yours, and what they cost

### Port-forwarding GWatch to the internet

Do not do this. It is the single worst thing you can do with GWatch, and it is
explicitly against our advice.

A forwarded port is found by internet-wide scanners within hours, not months. If
you have no password, everything above is handed to anonymous strangers. If you
do have one, it travels over plain HTTP where anyone on the path can read it,
and a single credential on a public hostname is thin protection for something
that can run commands and hold your SMTP password. GWatch's rate limiting blunts
a brute-force attempt; it is a backstop, not a perimeter, and it says so.

[`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) covers the two ways to do this properly —
a private network such as Tailscale or WireGuard, or a reverse proxy with TLS
plus a second layer in front of the sign-in.

### Leaving authentication off once GWatch is on the LAN

"It's only my home network" holds until a guest phone, a smart TV, a rented
IoT camera or a compromised laptop is on that network too. LAN access is not
authentication. If GWatch listens anywhere but loopback, create an account.

### Weak or reused passwords

Passwords are stored as argon2id hashes, which protects them if the database
leaks. It does nothing about a password that is guessable, or one you also use
somewhere that has already been breached. GWatch cannot tell the difference
between you and someone who knows your password.

The legacy shared access password is worse still: one password for everyone,
full administrator rights, and no name in the audit log. It exists so upgrades
do not break. Accounts replace it, and on anything reachable beyond loopback you
should be using accounts.

### API keys and endpoint tokens

An API key is shown once and stored as a digest, so a leaked key is your copy
leaking, not ours. A `readwrite` key can change your monitoring; a `read` key
cannot. Handing out `readwrite` because it was easier is a choice with a cost.

Custom endpoint tokens are stored **in the clear** in `gwatch.db`, because the
endpoint has to compare them. So is the action configuration of every trigger,
which means a Slack or Teams webhook URL, an ntfy or Pushover credential, or any
token you paste into a webhook action is readable by anyone who can read that
file. Endpoints are also exempt from the sign-in by design, so an endpoint with
no token — or one exposed through a reverse proxy you did not think about — is
an open door to whatever action it runs.

### Agent tokens on machines you do not control

An agent token can do exactly one thing: submit one machine's hardware readings.
It cannot read your nodes, your settings, or another machine's data, and a
reading is always filed under the machine that sent it. That is the whole reason
the token is cheap to lose — someone holding it can post fake readings for that
one machine and nothing else.

It is still a credential for your GWatch server, it ends up in the service
registration and shell history of whatever machine you install it on, and it
travels in the clear if you give the agent an `http://` URL. Put GWatch behind
HTTPS before you run an agent across a network you do not trust, and revoke
tokens for machines you no longer control.

### Stored SMTP credentials and backup passwords

Your SMTP password, the legacy access password and your scheduled-backup
password are encrypted in `gwatch.db` with the `gwatch.key` file created beside
it. That protects the database if it is copied without the key file — a stolen
backup drive, a database moved to another machine.

It does not protect anything against someone who can read both files, because
the program running as that service account has to be able to read them too.
Encryption at rest with a local key is a boundary against copied files, not
against a compromised machine. If someone has administrator access to the
computer, or the data directory's permissions let any user read it, treat those
credentials as theirs.

The same goes for backup archives. They are properly encrypted (Argon2id +
AES-256-GCM) and contain your settings in the clear inside that encryption, so
the archive is exactly as strong as the password you chose. Pick a weak backup
password and you have published your SMTP credentials to anyone who gets a copy.

### Custom checks and triggers you did not write

Anyone who can create or edit a custom check, trigger or endpoint can run
arbitrary code on the GWatch machine as the service. Copying a trigger recipe
from the internet is running someone else's code as a service account. Read it
first.

## Where that leaves us

Every item above is a choice you make in your own environment, on hardware we
have no access to, on a network we cannot see. GWatch is self-hosted; JX
Holdings, LLC runs no server, holds no copy of your data, and has no ability to
observe, prevent, detect, or repair a misconfiguration you make.

**The risk of those choices is yours.** To the fullest extent permitted by
applicable law, JX Holdings, LLC, Jeffrey Guntly, Garrett Guntly, and any other
contributor are not liable for any loss or damage arising from how you deploy,
configure, expose, or secure GWatch — including unauthorised access, exposure or
theft of credentials or monitoring data, commands run through triggers,
endpoints or custom checks, compromise of the host machine or other machines on
your network, or any outage you were not alerted to.

The software is provided "as is", without warranty of any kind. That is
[`LICENSE`](../LICENSE) Section 5, and this page does not narrow it. The broader
limitation of liability, and the fact that GWatch is not a safety-critical or
life-safety system, are in [`TERMS.md`](TERMS.md).

## Recommended baseline

None of this is hard, and it takes about ten minutes.

1. **Create an account** under Settings › Users & access before GWatch listens
   anywhere but `127.0.0.1` — and give yourself a password you do not use
   elsewhere. Use viewer accounts and `read` API keys for anything that only
   needs to look.
2. **Keep it on the LAN.** Leave the listener on `127.0.0.1` if only this
   computer needs it. Never forward the port from your router to the internet.
3. **For remote access, use a private network or a proxy with TLS.** Tailscale
   or WireGuard first; a reverse proxy terminating HTTPS with another layer in
   front of the sign-in if you must publish a hostname. See
   [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md).
4. **Restrict the data directory.** `C:\ProgramData\GWatch` (or your
   `--data-dir`) holds `gwatch.db`, `gwatch.key`, the logs and the backups.
   Limit it to administrators and the service account, and store backup archives
   somewhere with the same care.
5. **Give tokens one job.** One endpoint token per endpoint, one agent token per
   machine, and revoke either the moment it is no longer needed — revocation
   takes effect on the next request and the audit log keeps the name so you can
   still read back what it did.
6. **Keep it updated.** Check Settings › Updates from time to time; updates
   install only if their ed25519 signature verifies against the key pinned in
   the running binary, so an altered download is refused rather than installed
   with a warning.
7. **Read the audit log occasionally.** Failed sign-ins are recorded with the
   address they came from. It is the cheapest alarm you will ever fit.

If a credential does leak, [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md#if-a-key-or-password-leaks)
has the three steps: revoke or reset, read the audit log, and rotate the SMTP,
backup and endpoint secrets if the leaked credential was an administrator's.

## Related

- [`REMOTE-ACCESS.md`](REMOTE-ACCESS.md) — reaching GWatch from outside safely
- [`INSTALL.md`](INSTALL.md) — the Windows install, including the network page
- [`HARDWARE.md`](HARDWARE.md) — the agent's trust model in full
- [`TERMS.md`](TERMS.md) — terms of use, warranty and liability
- [`PRIVACY.md`](PRIVACY.md) — what is stored, where, and what leaves the machine
