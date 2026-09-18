VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build windows test test-race cover fmt fmt-check vet tidy-check web-check ci run

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch .

windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch.exe .

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

ci: fmt-check vet tidy-check test-race

run:
	go run . run --data-dir ./data
