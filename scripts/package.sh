#!/usr/bin/env bash
# Build a self-contained release tarball for one OS/architecture.
#
#   scripts/package.sh <linux|darwin> <amd64|arm64>
#     ->  dist/cronova_<os>_<arch>.tar.gz
#
# The tarball bundles the static binaries plus everything the platform installer
# needs, so the one-click bootstrap can install with zero build tools present.
set -euo pipefail

os="${1:?usage: package.sh <linux|darwin> <amd64|arm64>}"
arch="${2:?usage: package.sh <linux|darwin> <amd64|arm64>}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# CRONOVA_VERSION pins the baked/reported version (reproducible builds, local
# mirror testing); otherwise derive it from the current tag.
ver="${CRONOVA_VERSION:-$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo dev)}"

case "$os" in linux|darwin) ;; *) echo "unsupported os: $os" >&2; exit 1 ;; esac
case "$arch" in amd64|arm64) ;; *) echo "unsupported arch: $arch" >&2; exit 1 ;; esac

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

echo "==> building cronova $ver for $os/$arch"
export CGO_ENABLED=0 GOOS="$os" GOARCH="$arch"
ldflags="-s -w -X main.version=$ver"
go build -C "$root" -trimpath -ldflags "$ldflags" -o "$stage/cronova"          ./cmd/cronova
go build -C "$root" -trimpath -ldflags "$ldflags" -o "$stage/cronova-executor" ./cmd/cronova-executor

# assets the installer expects, at the same relative layout as the repo. Each
# tarball carries only its platform's installer + service file.
mkdir -p "$stage/deploy" "$stage/dags" "$stage/docs"
cp "$root"/deploy/cronova.env.example "$stage/deploy/"
if [[ "$os" == "darwin" ]]; then
  cp "$root"/deploy/install-macos.sh "$root"/deploy/com.cronova.plist "$root"/deploy/com.cronova.executor.plist "$stage/deploy/"
  chmod +x "$stage/deploy/install-macos.sh"
else
  cp "$root"/deploy/install.sh "$root"/deploy/cronova.service "$root"/deploy/cronova-executor.service "$stage/deploy/"
  chmod +x "$stage/deploy/install.sh"
fi
cp "$root"/cronova.yaml.example "$stage/"
cp "$root"/dags/*.yaml          "$stage/dags/"
cp "$root"/docs/DEPLOY.md       "$stage/docs/"
echo "$ver" > "$stage/VERSION"

mkdir -p "$root/dist"
out="$root/dist/cronova_${os}_${arch}.tar.gz"
tar -C "$stage" -czf "$out" .
echo "==> $out"
