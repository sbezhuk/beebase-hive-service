## Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /src

# git is needed to fetch github.com/sbezhuk/beebase-common - a private
# module (see the RUN below), resolved via a direct, authenticated git
# fetch rather than the public module proxy. Alpine's base image
# doesn't ship it.
RUN apk add --no-cache git

COPY go.mod go.sum ./

# github.com/sbezhuk/beebase-common is a private GitHub repo, so it's
# excluded from the public module proxy/checksum database (GOPRIVATE)
# and fetched directly via git instead, authenticated with a
# short-lived, read-only token supplied only as a BuildKit secret -
# never a build ARG/ENV, so it can never end up in an image layer or
# this Dockerfile, and it's gone the moment this RUN instruction ends
# (BuildKit mounts secrets into a tmpfs scoped to the one command).
# GIT_CONFIG_COUNT/_KEY_0/_VALUE_0 pass the credential to git as
# process-local config - never written to ~/.gitconfig - so nothing
# token-related persists once `go mod download` returns.
RUN --mount=type=secret,id=github_token,required=true \
    GOPRIVATE=github.com/sbezhuk/beebase-common \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0="url.https://x-access-token:$(cat /run/secrets/github_token)@github.com/.insteadOf" \
    GIT_CONFIG_VALUE_0="https://github.com/" \
    go mod download

COPY . .

# TARGETOS/TARGETARCH are populated automatically by BuildKit to match
# the requested --platform (e.g. `docker buildx build --platform
# linux/arm64`); with no --platform given they default to the host's own
# platform, so a plain local `docker build`/`docker compose build` is
# unaffected and keeps building for the machine it runs on.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

## Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 10001 beebase

COPY --from=builder /out/server /usr/local/bin/server

USER beebase

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server"]
