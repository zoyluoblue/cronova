#!/usr/bin/env bash
# Install cronova as a native systemd service on Debian/Ubuntu.
# Idempotent: safe to re-run to upgrade the binary or refresh the unit.
#
#   sudo ./deploy/install.sh [path-to-cronova-binary]
#
# If no binary path is given it looks for ./dist/cronova then ./cronova.
#
# Env knobs (used by the one-click bootstrap; all optional):
#   CRONOVA_ADMIN_USER=admin        first-admin username    (default: admin)
#   CRONOVA_ADMIN_PASSWORD=...       first-admin password    (default: random)
#   CRONOVA_START=1                  enable + start the service after install
set -euo pipefail

SVC_USER=cronova
BIN_DST=/usr/local/bin/cronova
CONF_DIR=/etc/cronova
STATE_DIR=/var/lib/cronova
DAGS_DIR="$STATE_DIR/dags"
LOG_DIR=/var/log/cronova
UNIT=/etc/systemd/system/cronova.service

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $EUID -ne 0 ]]; then
  echo "error: run as root (sudo $0)" >&2
  exit 1
fi

# 1. locate the binary
BIN_SRC="${1:-}"
if [[ -z "$BIN_SRC" ]]; then
  for c in "$SRC_DIR/dist/cronova" "$SRC_DIR/cronova"; do
    [[ -x "$c" ]] && BIN_SRC="$c" && break
  done
fi
if [[ -z "$BIN_SRC" || ! -x "$BIN_SRC" ]]; then
  echo "error: no cronova binary found. Build one first:" >&2
  echo "         make release        # -> dist/cronova  (needs Go 1.26)" >&2
  echo "       or pass the path:  sudo $0 /path/to/cronova" >&2
  exit 1
fi

echo "==> installing cronova from: $BIN_SRC"

# 2. dedicated system user
if ! id -u "$SVC_USER" >/dev/null 2>&1; then
  useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SVC_USER"
  echo "==> created system user '$SVC_USER'"
fi

# 3. binaries (executor is optional, for crash-recovery mode)
install -m 0755 "$BIN_SRC" "$BIN_DST"
echo "==> installed binary -> $BIN_DST"
for e in "$SRC_DIR/dist/cronova-executor" "$SRC_DIR/cronova-executor"; do
  if [[ -x "$e" ]]; then
    install -m 0755 "$e" /usr/local/bin/cronova-executor
    echo "==> installed binary -> /usr/local/bin/cronova-executor"
    break
  fi
done

# 4. directories
install -d -m 0755 "$CONF_DIR"
install -d -o "$SVC_USER" -g "$SVC_USER" -m 0750 "$STATE_DIR" "$DAGS_DIR" "$LOG_DIR"

# 5. first-time setup (only when there's no config yet — upgrades keep it).
#    `cronova init` writes cronova.yaml + cronova.env (the admin seed). It runs
#    interactively when a terminal is attached — even under `curl | sudo bash`,
#    via /dev/tty — and otherwise takes defaults + CRONOVA_* env.
if [[ ! -f "$CONF_DIR/cronova.yaml" ]]; then
  init=("$BIN_DST" init -config "$CONF_DIR/cronova.yaml" -env "$CONF_DIR/cronova.env")
  if [[ "${CRONOVA_NONINTERACTIVE:-0}" != "1" ]] && [[ -e /dev/tty ]] && ( : </dev/tty ) 2>/dev/null; then
    "${init[@]}" </dev/tty
  else
    "${init[@]}" -yes
  fi
fi

# 6. seed example DAGs only if the dags dir is empty (don't clobber console edits)
if [[ -z "$(ls -A "$DAGS_DIR" 2>/dev/null)" ]]; then
  if compgen -G "$SRC_DIR/dags/*.yaml" >/dev/null; then
    install -o "$SVC_USER" -g "$SVC_USER" -m 0644 "$SRC_DIR"/dags/*.yaml "$DAGS_DIR"/
    echo "==> seeded example DAGs into $DAGS_DIR"
  fi
fi

# 7. systemd unit
install -m 0644 "$SRC_DIR/deploy/cronova.service" "$UNIT"
systemctl daemon-reload
echo "==> installed unit -> $UNIT"

# 8. optionally start now (one-click path sets CRONOVA_START=1)
started=0
if [[ "${CRONOVA_START:-0}" == "1" ]]; then
  systemctl enable --now cronova
  started=1
fi

# --- summary ---------------------------------------------------------------
echo
# (the admin credentials, incl. any generated password, were printed by
#  'cronova init' above — scroll up to save them.)
if [[ $started -eq 1 ]]; then
  echo "cronova is running."
  echo "  systemctl status cronova   |   journalctl -u cronova -f"
else
  echo "cronova installed (not started). Start it with:"
  echo "  systemctl enable --now cronova"
fi
echo "  reconfigure anytime:  sudo cronova init -config $CONF_DIR/cronova.yaml -env $CONF_DIR/cronova.env"
echo
echo "Tasks run with the HOST's interpreters — install python3 / java / psql / etc."
echo "as needed and make sure they're on the PATH set in $UNIT."
