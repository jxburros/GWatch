# GWatch container image — see docs/DOCKER.md for how to run it.
#
# Two stages: the first compiles the single static binary the same way
# `make build` does (CGO_ENABLED=0, so it needs no C library at run time), the
# second copies just that binary into a small Alpine image with the few
# things GWatch wants around it: CA roots for HTTPS checks, `ping` for the
# "system" ping method, and time-zone data so schedules read correctly.
#
# Build with `make docker`, or by hand:
#   docker build --build-arg VERSION=$(cat VERSION) -t gwatch .

# --platform=$BUILDPLATFORM: compile natively and cross-compile for the target
# (TARGETOS/TARGETARCH come from buildx), rather than running the Go toolchain
# under emulation when publishing the arm64 image.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

# The version the binary reports. Left empty, it is read from the VERSION file
# so a plain `docker build .` still says the same thing as `make build`.
ARG VERSION=
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Download modules before copying the rest so a source-only change does not
# repeat the download.
COPY go.mod go.sum ./
RUN go mod download

# Everything not listed in .dockerignore. The web interface (web/) is embedded
# into the binary by //go:embed, so it has to be in the build context; the
# same goes for any other embedded directory added later.
COPY . .

RUN set -eu; \
    version="${VERSION:-$(tr -d ' \t\r\n' < VERSION)}"; \
    CGO_ENABLED=0 GOOS="${TARGETOS:-}" GOARCH="${TARGETARCH:-}" \
      go build -trimpath -ldflags "-s -w -X main.version=${version}" -o /out/gwatch .

FROM alpine:3.22

ARG VERSION=
LABEL org.opencontainers.image.title="GWatch" \
      org.opencontainers.image.description="Monitoring of home network devices, servers and websites, with a web interface." \
      org.opencontainers.image.source="https://github.com/jxburros/GWatch" \
      org.opencontainers.image.documentation="https://github.com/jxburros/GWatch/blob/main/docs/DOCKER.md" \
      org.opencontainers.image.licenses="LicenseRef-GWatch-Community-License-1.0" \
      org.opencontainers.image.version="${VERSION}"

# ca-certificates: HTTPS and certificate checks need the public CA roots.
# iputils: the `ping` command, used when Settings › General › Ping is set to
#   "system" (see docs/DOCKER.md for the other two ways to get ICMP working).
# tzdata: so a TZ environment variable is honoured by schedules and reports.
RUN apk add --no-cache ca-certificates iputils tzdata \
    && addgroup -g 7230 -S gwatch \
    && adduser -u 7230 -S -G gwatch -h /data -s /sbin/nologin gwatch \
    && mkdir -p /data \
    && chown gwatch:gwatch /data

COPY --from=build /out/gwatch /usr/local/bin/gwatch
COPY LICENSE /usr/share/doc/gwatch/LICENSE

# /data holds gwatch.db (and its -wal/-shm files), gwatch.key, logs/ and
# backups/. Mount a volume here; the database is useless without the key
# beside it, so keep them together.
ENV GWATCH_DATA_DIR=/data \
    GWATCH_LISTEN=0.0.0.0:7230
VOLUME /data
EXPOSE 7230

USER gwatch
WORKDIR /data

# /api/health needs no credential. BusyBox wget is part of the base image;
# curl is not.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:7230/api/health || exit 1

ENTRYPOINT ["gwatch"]
CMD ["run"]
