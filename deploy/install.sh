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

# 5. config (never overwrite an existing one)
if [[ ! -f "$CONF_DIR/cronova.yaml" ]]; then
  install -m 0644 "$SRC_DIR/cronova.yaml.example" "$CONF_DIR/cronova.yaml"
  echo "==> seeded $CONF_DIR/cronova.yaml"
fi

# 5b. secrets: seed cronova.env with an admin password on first install only.
#     Use the provided password, or generate a random one and enable auth so a
#     reachable console is never left open. Re-runs never touch an existing file.
GEN_PW=""
if [[ ! -f "$CONF_DIR/cronova.env" ]]; then
  admin_user="${CRONOVA_ADMIN_USER:-admin}"
  if [[ -n "${CRONOVA_ADMIN_PASSWORD:-}" ]]; then
    admin_pw="$CRONOVA_ADMIN_PASSWORD"
  else
    set +o pipefail
    admin_pw="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)"
    set -o pipefail
    GEN_PW="$admin_pw"
  fi
  umask 077
  cat > "$CONF_DIR/cronova.env" <<EOF
CRONOVA_ADMIN_USER=$admin_user
CRONOVA_ADMIN_PASSWORD=$admin_pw
# CRONOVA_SECURE_COOKIE=true   # enable when serving behind HTTPS
EOF
  chmod 600 "$CONF_DIR/cronova.env"
  # secure default: require login (the env just seeded an admin)
  sed -i 's/^\([[:space:]]*\)enabled: false/\1enabled: true/' "$CONF_DIR/cronova.yaml"
  echo "==> seeded $CONF_DIR/cronova.env and enabled auth"
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
if [[ $started -eq 1 ]]; then
  echo "cronova is running. Console: http://$(hostname -I 2>/dev/null | awk '{print $1}'):8090"
  echo "  systemctl status cronova   |   journalctl -u cronova -f"
else
  echo "cronova installed (not started). Start it with:"
  echo "  systemctl enable --now cronova"
  echo "  console: http://<server>:8090"
fi
if [[ -n "$GEN_PW" ]]; then
  echo
  echo "  ┌─ admin login (generated — save it) ───────────────────"
  echo "  │  user:     ${CRONOVA_ADMIN_USER:-admin}"
  echo "  │  password: $GEN_PW"
  echo "  └────────────────────────────────────────────────────────"
  echo "  change it later: cronova users passwd ${CRONOVA_ADMIN_USER:-admin} -db $STATE_DIR/cronova.db"
fi
echo
echo "Tasks run with the HOST's interpreters — install python3 / java / psql / etc."
echo "as needed and make sure they're on the PATH set in $UNIT."
