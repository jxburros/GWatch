# Reaching GWatch from outside your network

You are away from home and want to know whether the house is still online. This page
is about doing that safely.

## The short version

**GWatch does not become an internet-facing service.** It has no hosted relay, no
tunnel helper and no account on anyone else's server. What it gives you instead is a
properly scoped read-only identity — a viewer account or a read-only API key — and the
expectation that you bring your own way in: a VPN, or a reverse proxy with TLS that you
control.

That choice is deliberate. A monitoring service that runs commands on your machine,
holds your SMTP password and can restore a database from an uploaded file is not
something to expose to the open internet, and shipping a tunnel would make it easy to
do exactly that by accident. Scoping the credential is a few hundred lines; being an
internet-facing service safely is a permanent, much larger commitment.

**Never port-forward GWatch's HTTP port to the internet.** Plain HTTP means your
password or API key crosses the internet in the clear, and the port then answers to
every scanner on the planet. If you take one thing from this page, take that.

## Step 1: choose a credential

In the web interface, on the computer GWatch runs on, open **Settings › Users & access**.

**A viewer account** is for you, in a browser, on your phone. It can see dashboards,
charts, history, incidents and the audit log. Every change — nodes, checks, settings,
backups, automation — is refused with an explanation.

**A read-only API key** is for something other than a browser: a home-automation
dashboard, a status widget, a script. Send it as `Authorization: Bearer gw_…` or
`X-API-Key: gw_…`.

A key of either scope is refused, always, on: settings, backups and restores, updates,
user accounts, API keys, the configuration export, the service log, and anything that
runs a trigger, a custom endpoint or an action on the machine. A `read` key is refused
on every write as well. The full list is in [`API.md`](API.md#authentication-and-roles).

Prefer a viewer account for yourself and a read-only key for machines. Give a key one
job and revoke it the moment that job ends — revoking is immediate, and the audit log
keeps the key's name so you can still read back what it did.

## Step 2: choose a way in

### A private network (recommended)

**Tailscale** or another WireGuard mesh. Install it on the machine running GWatch and
on your phone, and GWatch stays bound to its own machine while your phone is simply on
the same private network. Nothing is published, nothing is forwarded, and the traffic
is encrypted end to end.

With Tailscale, leave `general.remoteAccess` off if the only device that needs GWatch
is the one running it, or turn it on and reach GWatch at the machine's Tailscale
address. `tailscale serve` will also put HTTPS in front of it for you.

A plain **WireGuard** tunnel to your home router does the same job with more setup.

This is the recommendation. It keeps GWatch off the public internet entirely, and the
credential from step 1 is then a second lock rather than the only one.

### A reverse proxy with TLS

If you must publish GWatch on a hostname, terminate TLS in front of it and never expose
the raw port. With Caddy, the whole configuration is:

```caddyfile
gwatch.example.com {
    reverse_proxy 127.0.0.1:7230
}
```

Caddy obtains and renews the certificate itself. Turn on `general.remoteAccess` only if
the proxy is on a different machine; when the proxy runs alongside GWatch, leave it off
and let the proxy reach `127.0.0.1:7230`, so the port is never open on the network.

Over HTTPS the session cookie is issued with `Secure` set, so it is never sent in the
clear.

Three things to do if you go this way:

- Put another layer in front of the sign-in — the proxy's own basic auth, an IP
  allow-list, or an identity provider. A single password on a public hostname is thin.
- Keep the proxy's access log, and keep an eye on GWatch's own audit log: failed
  sign-ins are recorded with the address they came from.
- Do not forward `/hook/…` unless you mean to. Custom endpoints are exempt from the
  sign-in by design, because other devices call them with their own token, and a
  publicly reachable hook is only as strong as that token.

### What not to do

- Do not forward port 7230 (or whatever you bound) from your router to the internet.
- Do not rely on the legacy shared access password on a public hostname. It is one
  password for everyone, it grants full administrator access, and it leaves no trace of
  who used it. Accounts replace it.
- Do not hand out a `readwrite` key so something can "just read a bit". Read-only is
  the default for a reason.

## Rate limiting

GWatch limits, per client IP:

- **failed credentials** — sign-ins, API keys and access-password attempts — to 10 per
  minute; and
- **API-key requests from off the machine** to 300 per minute.

Both answer `429` with `Retry-After`. The counters live in memory and reset when the
service restarts, which is the right trade-off for a single-machine service: they blunt
a brute-force attempt without ever locking you out of your own monitor for good.

They are a backstop, not a perimeter. A credential on a public hostname is still a
credential on a public hostname.

## If a key or password leaks

1. Revoke the key in Settings › Users & access, or reset the account's password — a
   reset signs every browser of that account out immediately.
2. Read the audit log (Audit › Event log). Every change carries the actor who made it,
   so you can see what the credential did while it was out.
3. If the leaked credential was an administrator, treat the SMTP password, the
   scheduled-backup password and any endpoint tokens as exposed too, and change them.
