#!/usr/bin/env bash
# Build a self-contained release tarball for one Linux architecture.
#
#   scripts/package.sh <amd64|arm64>   ->  dist/cronova_linux_<arch>.tar.gz
#
# The tarball bundles the static binaries plus everything deploy/install.sh
# needs, so the one-click bootstrap can install with zero build tools present.
set -euo pipefail

arch="${1:?usage: package.sh <amd64|arm64>}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ver="$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo dev)"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

echo "==> building cronova $ver for linux/$arch"
export CGO_ENABLED=0 GOOS=linux GOARCH="$arch"
go build -C "$root" -trimpath -ldflags "-s -w" -o "$stage/cronova"          ./cmd/cronova
go build -C "$root" -trimpath -ldflags "-s -w" -o "$stage/cronova-executor" ./cmd/cronova-executor

# assets install.sh expects, at the same relative layout as the repo
mkdir -p "$stage/deploy" "$stage/dags" "$stage/docs"
cp "$root"/deploy/cronova.service "$root"/deploy/install.sh "$root"/deploy/cronova.env.example "$stage/deploy/"
cp "$root"/cronova.yaml.example "$stage/"
cp "$root"/dags/*.yaml          "$stage/dags/"
cp "$root"/docs/DEPLOY.md       "$stage/docs/"
echo "$ver" > "$stage/VERSION"
chmod +x "$stage/deploy/install.sh"

mkdir -p "$root/dist"
out="$root/dist/cronova_linux_${arch}.tar.gz"
tar -C "$stage" -czf "$out" .
echo "==> $out"
