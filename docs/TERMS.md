# Terms of use

These are the terms you accept when you install or run GWatch. GWatch is
published by JX Holdings, LLC and was developed by Jeffrey Guntly and Garrett
Guntly.

> **Plain-language document, not legal advice.** This page is written the same
> way as the rest of GWatch's documentation: in ordinary English, so you can
> actually read it. It has not been reviewed by a lawyer, it is not legal
> advice, and it is not a substitute for advice about your own situation. If
> you are deploying GWatch somewhere the answer matters — a business, a client's
> network, a regulated environment — talk to your own counsel.

## Three documents, three jobs

GWatch is governed by three separate documents, and it is worth knowing which
one answers which question, because people usually go looking in the wrong one.

| Document | What it governs |
| --- | --- |
| [`LICENSE`](../LICENSE) | Copying, modifying, and distributing the code |
| [`TRADEMARKS.md`](../TRADEMARKS.md) | Using the GWatch name and logo |
| This page | Using the software |

All three apply at once. Nothing here grants you rights the license does not,
and nothing here takes away rights the license does grant. If this page and
[`LICENSE`](../LICENSE) ever appear to disagree, `LICENSE` controls — it is the
document that carries the actual grant.

One thing worth repeating from the license because people assume otherwise:
GWatch is **source-available, not open source**. You may use it, modify it,
host it, and build a paid deployment, support, or customization business around
it. You may not resell the software itself, or offer it essentially unmodified
as a paid hosted service. See `LICENSE` Section 2 for where that line sits.

## Who may use GWatch

Anyone, for any purpose, subject to these terms and the license. There is no
account to create, no key to buy, and no activation — GWatch never contacts JX
Holdings, so there is nothing for us to approve or refuse.

If you install GWatch on behalf of an employer or a client, you are agreeing to
these terms on their behalf as well as your own, and you should be sure you are
allowed to do that.

## Acceptable use: only probe what is yours

This is the part that matters most, and it is not boilerplate.

GWatch actively probes network infrastructure. Its checks send ICMP echo
requests, open TCP connections, make HTTP and HTTPS requests, perform DNS
lookups, and complete TLS handshakes to read certificates — on a schedule, over
and over, for as long as the check exists. Against a host you own, that is a
monitor. Against a host you do not own, the same traffic can look like
reconnaissance, and in many places it is unlawful regardless of your intent.

**You are responsible for making sure you own, operate, or have documented
permission to probe every host, address, and name you point GWatch at.** That
includes:

- Hosts on someone else's network, including a cloud or hosting provider's — an
  acceptable-use policy usually forbids unsolicited scanning even of a machine
  you rent.
- Public websites and APIs you do not run. A 60-second HTTP check against
  somebody else's endpoint is unsolicited automated traffic, whatever it is
  called in the UI.
- Anything belonging to your employer or a client that you have not been asked
  to monitor in writing.

You also agree not to use GWatch to:

- Load-test, flood, or otherwise degrade a system, whether or not it is yours.
  GWatch is a monitor, not a stress tool, and check intervals are not a rate
  budget to spend.
- Circumvent access controls, rate limits, bot protection, or authentication on
  any system.
- Monitor people rather than machines — GWatch records what infrastructure does,
  and turning it into surveillance of an individual is your decision and your
  liability, not something the software is for.
- Break any law that applies to you, or any contract or policy you are bound by.

GWatch will not stop you doing any of this. It has no allow-list, no upstream
service checking your targets, and no way for us to see what you monitor. The
only thing standing between the software and misuse is you, which is why this
section exists.

## Automation runs on your machine, as you

Triggers, custom endpoints, and the Custom script check type run commands on the
computer running GWatch, with the GWatch service's permissions. That is the
feature working as designed, and it means anyone who can create or edit one can
run arbitrary code on that machine.

You are responsible for what those commands do, for who you give administrator
access to, and for the consequences of a command that deletes the wrong thing.
Treat "can edit a trigger" as equivalent to "has a shell on this machine",
because it is.

## Not a safety-critical system

GWatch is a home and small-network monitor. It is not, and must not be relied
on as:

- a life-safety, medical, emergency-response, or alarm system;
- a fire, intrusion, environmental, or industrial-control monitor;
- a professionally monitored security service;
- a SIEM, an intrusion-detection system, or a compliance-evidence system;
- the sole control for anything where a missed or late alert causes injury,
  death, or serious loss.

GWatch runs on one computer you own. If that computer sleeps, loses power,
loses its network, or is simply off, monitoring stops — the timeline records the
gap honestly rather than pretending it was healthy, but the gap is still a gap.
Alert delivery depends on your own SMTP server and the internet between you and
it. None of that is a suitable foundation for a decision that has to be right.

If you need guaranteed monitoring or guaranteed notification, buy a service that
sells you that guarantee.

## No warranty

GWatch is provided **"as is", without warranty of any kind**, as stated in full
in [`LICENSE`](../LICENSE) Section 5. Nothing in these terms, in the
documentation, in the web interface, or in anything anyone says in an issue
thread creates a warranty.

In particular, we do not warrant that GWatch will detect every outage, that
alerts will arrive, that they will arrive on time, that checks will be accurate,
that the software is free of defects, or that it is fit for any particular
purpose of yours.

## Limitation of liability

To the fullest extent permitted by applicable law, JX Holdings, LLC, Jeffrey
Guntly, Garrett Guntly, and any other contributor are not liable for any
damages arising out of or connected to GWatch or your use of it. That includes
direct, indirect, incidental, special, consequential, and punitive damages, and
it specifically includes lost profits, lost data, business interruption, and
outages you were not alerted to.

This is the same position `LICENSE` Section 5 takes, restated where you are
likely to read it. It applies however the claim is framed — contract, tort,
negligence, or anything else — and whether or not anyone was told such damages
were possible.

Some jurisdictions do not allow the exclusion of certain warranties or the
limitation of certain damages. Where that is the case, these exclusions apply
only as far as that law allows, and nothing here is intended to limit liability
that cannot lawfully be limited.

Your security choices are a category of their own, and they have their own
page: see [`DISCLAIMER.md`](DISCLAIMER.md).

## Termination

Your rights under `LICENSE` end automatically if you violate its Sections 2, 3,
or 4 and do not fix the violation within 30 days of becoming aware of it — that
is `LICENSE` Section 6, and it is the mechanism that actually matters.

Your rights under these terms end at the same time, and also if you use GWatch
in a way this page forbids. On termination, stop using and distributing the
software. Because GWatch runs entirely on your own machine, there is no account
for us to close and no service for us to switch off — enforcement, if it ever
came to it, would be a legal matter rather than a technical one.

You can end these terms at any time by uninstalling GWatch. The sections about
warranty, liability, and acceptable use survive, as to anything that happened
while you were using it.

## Governing law

These terms are governed by the laws of **[jurisdiction]**, without regard to
its conflict-of-laws rules, and any dispute arising out of them will be brought
in the courts of **[jurisdiction]**.

> **To be filled in by JX Holdings, LLC.** This placeholder is deliberate — the
> correct jurisdiction is a decision for the company and its counsel, not
> something to guess. Replace both occurrences before publishing this document.

## Changes to these terms

We may change these terms as GWatch changes. The current version is always the
one in the repository at
[`docs/TERMS.md`](https://github.com/jxburros/GWatch/blob/main/docs/TERMS.md),
and its history is the git history of that file — there is no hidden revision
and no "we may update this at any time without notice" that you cannot verify.

Because GWatch runs on your machine and never checks in with us, a change to
these terms cannot reach out and alter software you already installed. Updated
terms apply to your continued use once they are published, and to any version
you install afterwards.

## Contact

Questions about these terms, about a commercial license under `LICENSE`
Section 2, or about trademark permission under
[`TRADEMARKS.md`](../TRADEMARKS.md): contact JX Holdings, LLC through the
project at <https://github.com/jxburros/GWatch>.

## Related

- [`LICENSE`](../LICENSE) — what you may do with the code
- [`TRADEMARKS.md`](../TRADEMARKS.md) — what you may do with the name and logo
- [`PRIVACY.md`](PRIVACY.md) — what GWatch does with data (short answer: keeps
  it on your machine)
- [`DISCLAIMER.md`](DISCLAIMER.md) — security choices and who carries the risk
