# The version lives in the VERSION file, so a local build, the installer and a
# release all say the same thing. Releases are cut by tagging v<VERSION>; CI
# checks the two agree before it publishes anything (see docs/RELEASING.md).
VERSION ?= $(shell tr -d ' \t\r\n' < VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build windows docker rsrc agent agent-all test test-race cover fmt fmt-check vet tidy-check web-check web-test ci run keygen sign verify-release mcp-build mcp-test mcp-fmt

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch .

windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch.exe .

# The container image, built from the Dockerfile for this machine's platform
# and tagged with the VERSION file (CI publishes the multi-arch one to
# ghcr.io/jxburros/gwatch on a release tag — see docs/DOCKER.md).
docker:
	docker build --build-arg VERSION=$(VERSION) -t gwatch:$(VERSION) -t gwatch:latest .

# The two Windows setup programs are built by Inno Setup, which only runs on
# Windows, so there is no make target for them — see scripts/build-installer.ps1
# (one command, both installers) or scripts/installer/README.md.

# Regenerates the .syso resource objects that give gwatch.exe and
# gwatch-agent.exe their icon. The outputs are committed, so this only needs
# running when scripts/installer/assets/gwatch.ico changes — but it is
# deterministic, so running it when nothing changed produces no diff.
ICON := scripts/installer/assets/gwatch.ico
rsrc:
	go run ./cmd/gwatch-rsrc -ico $(ICON) -out rsrc
	go run ./cmd/gwatch-rsrc -ico $(ICON) -out cmd/gwatch-agent/rsrc

# gwatch-agent runs on the machines being watched rather than on this one, so
# it is built for every platform someone might want to install it on. It is a
# separate binary on purpose: it carries no database, no web interface and no
# credential for GWatch beyond its own submit-only token.
AGENT_LDFLAGS := -s -w -X main.version=$(VERSION)

agent:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(AGENT_LDFLAGS)" -o dist/gwatch-agent ./cmd/gwatch-agent

agent-all:
	@set -e; for target in \
		windows/amd64 windows/arm64 \
		linux/amd64 linux/arm64 linux/arm \
		darwin/amd64 darwin/arm64; do \
		os=$${target%%/*}; arch=$${target##*/}; \
		ext=''; [ "$$os" = windows ] && ext='.exe'; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags "$(AGENT_LDFLAGS)" -o "dist/gwatch-agent-$$os-$$arch$$ext" ./cmd/gwatch-agent; \
	done

test:
	go vet ./... && go test ./...

# What CI runs: the race detector catches scheduler/logger data races.
test-race:
	go test -race -covermode=atomic -coverprofile=coverage.out -count=1 ./...

cover: test-race
	go tool cover -func=coverage.out

fmt:
	gofmt -w .

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi

vet:
	go vet ./...

tidy-check:
	@cp go.mod go.mod.ci && cp go.sum go.sum.ci; \
	go mod tidy; \
	status=0; \
	if ! diff -q go.mod go.mod.ci >/dev/null || ! diff -q go.sum go.sum.ci >/dev/null; then \
		echo "go.mod/go.sum are out of date; run 'go mod tidy'"; status=1; \
	fi; \
	mv go.mod.ci go.mod && mv go.sum.ci go.sum; \
	exit $$status

# The web assets are embedded with //go:embed, so the Go compiler never sees a
# syntax error in them. Needs node on PATH.
web-check:
	@find web -name '*.js' -type f | sort | xargs -n1 node --check

# The web test suite (tests/web/) exercises web/'s shared helpers and each
# view's happy path against web/mock.js, with jsdom as the only dependency —
# it lives outside web/ so //go:embed never ships it in the binary. Needs
# `npm ci` run once first (devDependencies only; see package.json).
web-test:
	npm test

# The MCP companion (mcp/) is a separate Go module with its own go.mod and its
# own version, so the root `./...` above never sees it — these targets are how
# it gets built and tested. It talks to GWatch over the JSON API with an API
# key and imports nothing from this module; see mcp/README.md.
MCP_VERSION := $(shell tr -d ' \t\r\n' < mcp/VERSION)

mcp-build:
	cd mcp && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(MCP_VERSION)" -o ../dist/gwatch-mcp ./cmd/gwatch-mcp

mcp-test:
	cd mcp && go vet ./... && go test ./...

mcp-fmt:
	cd mcp && gofmt -w .

ci: fmt-check vet tidy-check test-race mcp-test mcp-build web-test

run:
	go run . run --data-dir ./data

# Release signing (see docs/RELEASING.md). KEY defaults to release.key; leave it
# empty to take the key from the GWATCH_SIGNING_KEY environment variable, the
# way CI does.
KEY ?= release.key
KEYARG := $(if $(KEY),-key $(KEY),)

# One-time: generates the signing key and prints the line for release_keys.txt.
# release.key is secret — keep it offline, never commit it.
keygen:
	go run ./cmd/gwatch-sign keygen -out $(KEY)

# Writes dist/<asset>.sig next to every built binary.
sign:
	go run ./cmd/gwatch-sign sign $(KEYARG) dist/gwatch-*

# Verifies those signatures with the keys pinned in release_keys.txt, i.e. what
# an installed GWatch will do before it replaces itself.
verify-release:
	go run ./cmd/gwatch-sign verify dist/gwatch-*
