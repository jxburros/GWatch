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

A proxy on the same machine connects to GWatch from `127.0.0.1`, the address that
normally means "someone at this computer" and grants administrator access with no
sign-in. GWatch tells the two apart by the forwarding headers a proxy adds
(`X-Forwarded-For`, `Forwarded`, `X-Real-IP` and the like): a request carrying one is
treated as the remote client it is, and has to sign in. Caddy and `tailscale serve` add
`X-Forwarded-For` by default. nginx does not — add
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` to the `location` block,
or every visitor through it will be treated as sitting at the GWatch machine.

GWatch sees plain HTTP from the proxy, so the session cookie is not marked `Secure`; the
proxy's HTTPS still keeps it out of sight on the way. That is deliberate: a `Secure`
cookie set through `https://` would be withheld from the same host name over plain
HTTP, and could not then be replaced from there, so signing in over the LAN address
would bounce straight back to the sign-in screen. If the host name is only ever reached
over HTTPS, nothing is lost — and you can tick **GWatch is behind an HTTPS reverse
proxy** in Settings › Network access to have the cookie marked `Secure` after all. Leave
it off if anyone still opens GWatch over plain HTTP.

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

- **sign-in attempts** to 10 per minute for each account name, and to 30 per minute
  across all account names together. A correct password clears the first budget for
  that one account; it never clears the second, so signing in to your own account
  between guesses at someone else's buys nothing. Changing your password spends from
  the same budgets;
- **other failed credentials** — API keys and access-password attempts — to 10 per
  minute; and
- **API-key requests from off the machine** to 300 per minute.

All answer `429` with `Retry-After`. The counters live in memory and reset when the
service restarts, which is the right trade-off for a single-machine service: they blunt
a brute-force attempt without ever locking you out of your own monitor for good.

They are a backstop, not a perimeter. A credential on a public hostname is still a
credential on a public hostname.

### Behind a reverse proxy: `--trusted-proxies`

By default the "client IP" is the address that opened the connection to GWatch, and
forwarding headers are ignored — anyone can write an `X-Forwarded-For` header, and
believing it would let a guesser pick a fresh identity for every attempt. Behind a
reverse proxy, though, every visitor connects from the proxy's address, so they all
share one set of budgets: one person spending the sign-in budget gets `429` for
everybody coming through that proxy.

If GWatch sits behind a proxy you run, tell it which addresses are that proxy:

```sh
gwatch run --trusted-proxies 127.0.0.1,::1          # a proxy on the same machine
gwatch run --trusted-proxies 10.0.0.0/24            # a proxy elsewhere on the LAN
GWATCH_TRUSTED_PROXIES=127.0.0.1 gwatch run         # the same, from the environment
```

The value is a list of addresses and CIDR ranges, separated by commas. `gwatch install`
passes it on to the service (in `/etc/gwatch/gwatch.env` on Linux, when that file is
first written; add `GWATCH_TRUSTED_PROXIES=…` there yourself on an existing install).

Only for a request whose connection comes from one of those addresses does GWatch read
`X-Forwarded-For`, from the right: each proxy appends the address it was connected
from, so the rightmost entry that is not itself one of your trusted proxies is the
visitor. Entries further left were written by the visitor and are ignored. Without
`X-Forwarded-For`, `X-Real-IP` is used. If the header cannot be read, the proxy's own
address is used, as it is without the setting. Make sure the proxy appends to
`X-Forwarded-For` (Caddy and `tailscale serve` do; for nginx use
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`, or set `X-Real-IP` to
`$remote_addr`) and that nothing else can connect to GWatch from a trusted address.

The visitor's address is then what the rate limits count and what the audit log
records for a failed sign-in. It does not make anyone an administrator: a request that
came through a proxy is never treated as "someone at this computer", whatever address
it names.

List only proxies you control. Trusting an address that ordinary clients can connect
from lets them choose their own identity, and with it a fresh budget each time.

## If a key or password leaks

1. Revoke the key in Settings › Users & access, or reset the account's password — a
   reset signs every browser of that account out immediately.
2. Read the audit log (Audit › Event log). Every change carries the actor who made it,
   so you can see what the credential did while it was out.
3. If the leaked credential was an administrator, treat the SMTP password, the
   scheduled-backup password and any endpoint tokens as exposed too, and change them.
