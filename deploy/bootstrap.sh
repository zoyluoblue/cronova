#!/usr/bin/env bash
# One-click cronova installer for Linux (systemd) and macOS (launchd). Downloads
# a prebuilt release for this machine's OS/architecture and installs it as a
# native service.
#
#   curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
#
# Env knobs (all optional). Pass them through the pipe with `sudo -E`, e.g.
#   curl -fsSL <url> | CRONOVA_HTTP=127.0.0.1:9000 CRONOVA_NONINTERACTIVE=1 sudo -E bash
#
# Install process:
#   CRONOVA_VERSION=v0.1.0          install a specific release (default: latest)
#   CRONOVA_START=0                 install but don't start (default: start)
#   CRONOVA_NONINTERACTIVE=1        skip the wizard even with a TTY (defaults + env)
#   CRONOVA_BASE_URL=<origin>       download from a private mirror instead of GitHub
# Admin seed (-> cronova.env):
#   CRONOVA_ADMIN_USER=admin        seed this admin username
#   CRONOVA_ADMIN_PASSWORD=secret   seed this admin password (else one is generated)
# Server config (-> cronova.yaml, non-interactive install):
#   CRONOVA_HTTP=:8090              console/API listen addr (":8090"=all, "127.0.0.1:8090"=local)
#   CRONOVA_AUTH=true               require login for the console/API (default: true)
#   CRONOVA_SESSION_TTL=24h         login session lifetime
#   CRONOVA_SECURE_COOKIE=true      mark the session cookie Secure (set behind HTTPS)
#   CRONOVA_TICK=2s                 scheduler loop interval
#   CRONOVA_EXECUTOR=<gRPC target>  decoupled executor (empty = in-process)
set -euo pipefail

REPO="zoyluoblue/cronova"
VERSION="${CRONOVA_VERSION:-latest}"
: "${CRONOVA_START:=1}"; export CRONOVA_START

die() { echo "cronova: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run as root — pipe into 'sudo bash', e.g. curl -fsSL <url> | sudo bash"
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar  >/dev/null 2>&1 || die "tar is required"

# OS + arch -> release asset (cronova_<os>_<arch>.tar.gz) and the matching
# platform installer inside it.
case "$(uname -s)" in
  Linux)  os=linux;  installer="deploy/install.sh" ;;
  Darwin) os=darwin; installer="deploy/install-macos.sh" ;;
  *) die "unsupported OS: $(uname -s) (Linux/macOS only)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64/arm64 only)" ;;
esac

# sha256 tool differs by platform: sha256sum on Linux, `shasum -a 256` on macOS.
if command -v sha256sum >/dev/null 2>&1; then
  sha256_check() { sha256sum -c - ; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_check() { shasum -a 256 -c - ; }
else
  sha256_check() { return 2; } # no tool -> signal "skip" below
fi

# CRONOVA_BASE_URL overrides the download origin (private mirror / air-gapped /
# testing). It must host <tarball> and SHA256SUMS directly.
if [[ -n "${CRONOVA_BASE_URL:-}" ]]; then
  base="${CRONOVA_BASE_URL%/}"
elif [[ "$VERSION" == "latest" ]]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
tarball="cronova_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

echo "==> downloading $tarball ($VERSION)"
curl -fSL --proto '=https' --tlsv1.2 -o "$tmp/$tarball" "$base/$tarball" \
  || die "download failed — does release '$VERSION' have $tarball? see https://github.com/$REPO/releases"

# verify checksum when the release publishes SHA256SUMS
if curl -fsSL --proto '=https' -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" 2>/dev/null; then
  if ( cd "$tmp" && grep " $tarball\$" SHA256SUMS | sha256_check >/dev/null ); then
    echo "==> checksum OK"
  elif [[ $? -eq 2 ]]; then
    echo "cronova: warning — no sha256 tool (sha256sum/shasum), skipping verification" >&2
  else
    die "checksum verification failed for $tarball"
  fi
else
  echo "cronova: warning — SHA256SUMS not found, skipping verification" >&2
fi

tar -C "$tmp" -xzf "$tmp/$tarball"
echo "==> installing"
# not exec: keep the EXIT trap so the temp dir is cleaned up afterwards
bash "$tmp/$installer" "$tmp/cronova"
