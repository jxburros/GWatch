# GWatch

A simple network monitoring app with a web UI.

## Features

- **Ping checks** for reachability
- **HTTP/S checks** for endpoint status
- **DNS checks** for hostname resolution

HTTP checks intentionally reject localhost/private IP targets (set `GWATCH_ALLOW_PRIVATE_HTTP_TARGETS=true` to override).

## Run

```bash
go run .
```

Then open http://localhost:8080.

## Test

```bash
go test ./...
```
