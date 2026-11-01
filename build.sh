#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
# Linux desktop integration is deferred. This builds the portable backend only.
command -v go >/dev/null || { echo 'Install Go 1.26 or newer.' >&2; exit 1; }
command -v npm >/dev/null || { echo 'Install Node.js 22.12+ and npm.' >&2; exit 1; }
npm --prefix web ci
npm --prefix web run build
npm --prefix web test
go mod download
CGO_ENABLED=0 go test ./...
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w -X main.version=0.1.2' -o dist/dgx-hub-linux-amd64 ./cmd/dgx-hub
