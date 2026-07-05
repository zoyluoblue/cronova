#!/usr/bin/env bash
# One-click cronova installer. Downloads a prebuilt release for this machine's
# architecture and installs it as a systemd service.
#
#   curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
#
# Env knobs (all optional):
#   CRONOVA_VERSION=v0.1.0          install a specific release (default: latest)
#   CRONOVA_ADMIN_USER=admin        seed this admin username
#   CRONOVA_ADMIN_PASSWORD=secret   seed this admin password (else one is generated)
#   CRONOVA_START=0                  install but don't start (default: start)
set -euo pipefail

REPO="zoyluoblue/cronova"
VERSION="${CRONOVA_VERSION:-latest}"
: "${CRONOVA_START:=1}"; export CRONOVA_START

die() { echo "cronova: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run as root — pipe into 'sudo bash', e.g. curl -fsSL <url> | sudo bash"
[[ "$(uname -s)" == "Linux" ]] || die "Linux only (this is $(uname -s))"
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar  >/dev/null 2>&1 || die "tar is required"

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64/arm64 only)" ;;
esac

# CRONOVA_BASE_URL overrides the download origin (private mirror / air-gapped /
# testing). It must host <tarball> and SHA256SUMS directly.
if [[ -n "${CRONOVA_BASE_URL:-}" ]]; then
  base="${CRONOVA_BASE_URL%/}"
elif [[ "$VERSION" == "latest" ]]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
tarball="cronova_linux_${arch}.tar.gz"

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

echo "==> downloading $tarball ($VERSION)"
curl -fSL --proto '=https' --tlsv1.2 -o "$tmp/$tarball" "$base/$tarball" \
  || die "download failed — does release '$VERSION' have $tarball? see https://github.com/$REPO/releases"

# verify checksum when the release publishes SHA256SUMS
if curl -fsSL --proto '=https' -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" 2>/dev/null; then
  ( cd "$tmp" && grep " $tarball\$" SHA256SUMS | sha256sum -c - >/dev/null ) \
    || die "checksum verification failed for $tarball"
  echo "==> checksum OK"
else
  echo "cronova: warning — SHA256SUMS not found, skipping verification" >&2
fi

tar -C "$tmp" -xzf "$tmp/$tarball"
echo "==> installing"
# not exec: keep the EXIT trap so the temp dir is cleaned up afterwards
bash "$tmp/deploy/install.sh" "$tmp/cronova"
