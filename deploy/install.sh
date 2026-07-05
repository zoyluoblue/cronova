#!/usr/bin/env bash
# Install cronova as a native systemd service on Debian/Ubuntu.
# Idempotent: safe to re-run to upgrade the binary or refresh the unit.
#
#   sudo ./deploy/install.sh [path-to-cronova-binary]
#
# If no binary path is given it looks for ./dist/cronova then ./cronova.
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

# 3. binary
install -m 0755 "$BIN_SRC" "$BIN_DST"
echo "==> installed binary -> $BIN_DST"

# 4. directories
install -d -m 0755 "$CONF_DIR"
install -d -o "$SVC_USER" -g "$SVC_USER" -m 0750 "$STATE_DIR" "$DAGS_DIR" "$LOG_DIR"

# 5. config (never overwrite an existing one)
if [[ ! -f "$CONF_DIR/cronova.yaml" ]]; then
  install -m 0644 "$SRC_DIR/cronova.yaml.example" "$CONF_DIR/cronova.yaml"
  echo "==> seeded $CONF_DIR/cronova.yaml (edit me)"
fi
if [[ ! -f "$CONF_DIR/cronova.env" && -f "$SRC_DIR/deploy/cronova.env.example" ]]; then
  install -m 0600 "$SRC_DIR/deploy/cronova.env.example" "$CONF_DIR/cronova.env"
  echo "==> seeded $CONF_DIR/cronova.env (set CRONOVA_ADMIN_PASSWORD, chmod 600)"
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

cat <<EOF

cronova installed. Next:
  1. edit  $CONF_DIR/cronova.yaml        (set auth.enabled: true for a shared server)
  2. set   CRONOVA_ADMIN_PASSWORD in $CONF_DIR/cronova.env
  3. start:
       systemctl enable --now cronova
       systemctl status cronova
       journalctl -u cronova -f

  console: http://<server>:8090
  Tasks run with the HOST's interpreters — make sure python3 / java / psql / etc.
  are installed and on the PATH set in $UNIT.
EOF
