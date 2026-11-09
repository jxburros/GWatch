# Contributing to GWatch

Thank you for helping. GWatch is in beta, and reports from real networks are the
most useful thing you can give it.

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/jxburros/GWatch/issues/new/choose). For a bug,
say which version you run (**Settings › Updates** shows it), how you installed it
(Windows installer, Linux package, Docker, from source) and what you expected to
happen. **Settings › Monitor health** has the service log, which often holds the
answer. Take care not to paste passwords, tokens or API keys.

**Security problems are different: do not open a public issue.** See
[`SECURITY.md`](SECURITY.md).

Before asking for a feature, look at [`ROADMAP.md`](ROADMAP.md). It lists what is
planned and what GWatch has decided not to become, with the reasons.

## Changing the code

1. Read [`docs/development/DEVELOPMENT.md`](docs/development/DEVELOPMENT.md): how to
   run GWatch from source, the `make` targets and where things live.
2. For anything larger than a fix, open an issue first so the approach can be agreed
   before you spend time on it.
3. Keep a change to one subject, and add or update a test that would have caught
   the problem.
4. Run `make ci` before you open a pull request. It runs what the CI checks run
   locally: formatting, `go vet`, the Go tests with the race detector, the `mcp/`
   module, and the web test suites.
5. If you changed anything under the paths a release is built from, advance the
   version and add release notes, or the **Release version validation** check will
   fail. [`docs/development/RELEASING.md`](docs/development/RELEASING.md) explains
   how, and [`CHANGELOG.md`](CHANGELOG.md) shows the style.
6. If you changed how something behaves, update the document that describes it. The
   [documentation index](docs/README.md) says which one that is.

### What the project cares about

- **Calm by default.** A new alert, banner or prompt has to earn its place.
- **Nothing leaves the machine** unless the person configured it to.
- **The web interface has no build step and no dependencies,** and every screen
  must work from the keyboard and with a screen reader. CI runs axe on every route.
- **Comments say why,** not what.
- **Plain words** in the interface and the documents, in British spelling.

## Licence

GWatch is source-available under the [GWatch Community License](LICENSE), with a
separate [trademark policy](TRADEMARKS.md). Read both before contributing: they say
what may be done with the code and the name. Terms for contributions beyond that
licence have not been written down yet; if that matters for your contribution, ask
in an issue before you start.
