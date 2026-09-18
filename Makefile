VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build windows test run vet

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch .

windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gwatch.exe .

test:
	go vet ./... && go test ./...

run:
	go run . run --data-dir ./data

vet:
	go vet ./...
