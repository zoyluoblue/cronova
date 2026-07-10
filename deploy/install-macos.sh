#!/usr/bin/env bash
# Install cronova as a native launchd LaunchDaemon on macOS.
# Idempotent: safe to re-run to upgrade the binary or refresh the service.
#
#   sudo ./deploy/install-macos.sh [path-to-cronova-binary]
#
# If no binary path is given it looks for ./dist/cronova then ./cronova.
# The macOS counterpart of deploy/install.sh (which is Linux/systemd).
#
# Env knobs (used by the one-click bootstrap; all optional):
#   CRONOVA_ADMIN_USER=admin        first-admin username    (default: admin)
#   CRONOVA_ADMIN_PASSWORD=...       first-admin password    (default: random)
#   CRONOVA_START=1                  load + start the service after install
set -euo pipefail

LABEL=com.cronova
EXEC_LABEL=com.cronova.executor
BIN_DST=/usr/local/bin/cronova
CONF_DIR=/usr/local/etc/cronova
STATE_DIR=/usr/local/var/cronova
DAGS_DIR="$STATE_DIR/dags"
PROJECTS_DIR="$STATE_DIR/projects"
WORKSPACES_DIR="$STATE_DIR/workspaces"
RUN_DIR="$STATE_DIR/run"
LOG_DIR=/usr/local/var/log/cronova
PLIST="/Library/LaunchDaemons/$LABEL.plist"
EXEC_PLIST="/Library/LaunchDaemons/$EXEC_LABEL.plist"

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

[[ "$(uname -s)" == "Darwin" ]] || { echo "error: this installer is macOS-only (use deploy/install.sh on Linux)" >&2; exit 1; }
[[ $EUID -eq 0 ]] || { echo "error: run as root (sudo $0)" >&2; exit 1; }

# The daemon runs as the console user who invoked sudo — NOT root — so that tasks
# run unprivileged and the console can edit DAGs owned by that user. Falling back
# to root:wheel only when we truly can't tell (e.g. a root login shell).
SVC_USER="${SUDO_USER:-root}"
if [[ "$SVC_USER" == "root" ]]; then
  echo "warning: no SUDO_USER — the service (and its tasks) will run as root." >&2
  SVC_GROUP=wheel
else
  SVC_GROUP="$(id -gn "$SVC_USER")"
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
  echo "         make build          # -> ./cronova  (needs Go 1.26.5+)" >&2
  echo "       or pass the path:  sudo $0 /path/to/cronova" >&2
  exit 1
fi

EXEC_SRC=""
for e in "$SRC_DIR/dist/cronova-executor" "$SRC_DIR/cronova-executor"; do
  [[ -x "$e" ]] && EXEC_SRC="$e" && break
done
if [[ -z "$EXEC_SRC" ]]; then
  echo "error: cronova-executor is required for an installed service." >&2
  echo "       Build it with: make release" >&2
  exit 1
fi

echo "==> installing cronova from: $BIN_SRC  (service user: $SVC_USER:$SVC_GROUP)"

# 2. binaries (installed deployments use the standalone executor by default)
install -d -m 0755 "$(dirname "$BIN_DST")"
install -m 0755 "$BIN_SRC" "$BIN_DST"
echo "==> installed binary -> $BIN_DST"
install -m 0755 "$EXEC_SRC" /usr/local/bin/cronova-executor
echo "==> installed binary -> /usr/local/bin/cronova-executor"

# 3. directories (config read-only-ish; state + logs owned by the service user)
install -d -m 0755 "$CONF_DIR"
install -d -o "$SVC_USER" -g "$SVC_GROUP" -m 0750 "$STATE_DIR" "$DAGS_DIR" "$LOG_DIR"
install -d -o "$SVC_USER" -g "$SVC_GROUP" -m 0700 "$PROJECTS_DIR" "$WORKSPACES_DIR" "$RUN_DIR"

# 4. first-time setup (only when there's no config yet — upgrades keep it).
#    `cronova init` writes cronova.yaml + a credential-free env template, seeds
#    the admin hash directly in SQLite, and runs interactively
#    when a terminal is attached (even under `curl | sudo bash`, via /dev/tty).
if [[ ! -f "$CONF_DIR/cronova.yaml" ]]; then
  init=("$BIN_DST" init -config "$CONF_DIR/cronova.yaml" -env "$CONF_DIR/cronova.env")
  run_init() {
    env \
      CRONOVA_DB="$STATE_DIR/cronova.db" \
      CRONOVA_DAGS="$DAGS_DIR" \
      CRONOVA_LOGS="$LOG_DIR" \
      CRONOVA_PROJECTS="$PROJECTS_DIR" \
      CRONOVA_WORKSPACES="$WORKSPACES_DIR" \
      CRONOVA_KEY_FILE="$STATE_DIR/cronova.key" \
      CRONOVA_EXECUTOR="unix://$RUN_DIR/executor.sock" \
      "${init[@]}" "$@"
  }
  if [[ "${CRONOVA_NONINTERACTIVE:-0}" != "1" ]] && [[ -e /dev/tty ]] && ( : </dev/tty ) 2>/dev/null; then
    run_init </dev/tty
  else
    run_init -yes
  fi
  chown -R "$SVC_USER:$SVC_GROUP" "$STATE_DIR" "$LOG_DIR"
  chown root:wheel "$CONF_DIR/cronova.yaml" "$CONF_DIR/cronova.env"
  chmod 0644 "$CONF_DIR/cronova.yaml"
  chmod 0600 "$CONF_DIR/cronova.env"
fi
# The new plist no longer sources this file. Secure any legacy copy so tasks
# running as the service user cannot read an old bootstrap password.
if [[ -f "$CONF_DIR/cronova.env" ]]; then
  chown root:wheel "$CONF_DIR/cronova.env"
  chmod 0600 "$CONF_DIR/cronova.env"
fi

# 5. seed example DAGs only if the dags dir is empty (don't clobber console edits)
if [[ -z "$(ls -A "$DAGS_DIR" 2>/dev/null)" ]]; then
  if /bin/ls "$SRC_DIR"/dags/*.yaml >/dev/null 2>&1; then
    install -o "$SVC_USER" -g "$SVC_GROUP" -m 0644 "$SRC_DIR"/dags/*.yaml "$DAGS_DIR"/
    echo "==> seeded example DAGs into $DAGS_DIR"
  fi
fi

# 6. launchd daemon: render the plist with the service user, install to
#    /Library/LaunchDaemons, and (re)load it.
sed -e "s|__USER__|$SVC_USER|g" -e "s|__GROUP__|$SVC_GROUP|g" \
  "$SRC_DIR/deploy/$LABEL.plist" > "$PLIST"
sed -e "s|__USER__|$SVC_USER|g" -e "s|__GROUP__|$SVC_GROUP|g" \
  "$SRC_DIR/deploy/$EXEC_LABEL.plist" > "$EXEC_PLIST"
chown root:wheel "$PLIST" "$EXEC_PLIST"
chmod 0644 "$PLIST" "$EXEC_PLIST"
shasum -a 256 "$PLIST" "$EXEC_PLIST" > "$CONF_DIR/service-def.sha256"
chown root:wheel "$CONF_DIR/service-def.sha256"
chmod 0644 "$CONF_DIR/service-def.sha256"
echo "==> installed daemons -> $EXEC_PLIST, $PLIST"

# 7. optionally load + start now (one-click path sets CRONOVA_START=1).
#    `launchctl bootstrap` only returns whether the job LOADED, not whether the
#    program stayed up — so we must actively confirm the daemon is running, or a
#    port conflict / bad config / unreadable env would be reported as success
#    (the Linux path fails loudly via `systemctl enable --now`; match that).

# service_loaded: is a job with the label registered in the system domain?
service_loaded() { launchctl print "system/$1" >/dev/null 2>&1; }

confirm_service() {
  local label="$1" out
  for _ in $(seq 1 16); do
    out="$(launchctl print "system/$label" 2>/dev/null || true)"
    if printf '%s\n' "$out" | grep -q 'state = running' \
       && printf '%s\n' "$out" | grep -qE 'pid = [0-9]+'; then
      sleep 1
      out="$(launchctl print "system/$label" 2>/dev/null || true)"
      printf '%s\n' "$out" | grep -q 'state = running' && return 0
      break
    fi
    sleep 0.5
  done
  echo "error: launchd service $label did not stay running after load." >&2
  return 1
}

# start_service loads (or reloads) the daemon and returns 0 only once it is
# confirmed running. Prints the launchctl error to stderr on failure.
start_job() {
  local label="$1" plist="$2"
  # Tear down any existing job first (upgrade re-run). bootout is ASYNC — wait
  # for launchd to finish, else the following bootstrap races and fails.
  if service_loaded "$label"; then
    launchctl bootout "system/$label" 2>/dev/null || true
    for _ in $(seq 1 40); do service_loaded "$label" || break; sleep 0.25; done
  fi

  local err
  if err="$(launchctl bootstrap system "$plist" 2>&1)"; then
    launchctl enable "system/$label" 2>/dev/null || true
  else
    # older macOS (no bootstrap) or a transient race: try the legacy loader.
    launchctl unload "$plist" 2>/dev/null || true
    if ! err="$(launchctl load -w "$plist" 2>&1)"; then
      echo "error: launchctl could not load the service: $err" >&2
      return 1
    fi
  fi

  confirm_service "$label"
}

start_service() {
  # Never bounce an already-running executor during a scheduler upgrade: it owns
  # in-flight tasks. A fresh install loads it before the scheduler.
  if service_loaded "$EXEC_LABEL"; then
    confirm_service "$EXEC_LABEL" || return 1
  else
    start_job "$EXEC_LABEL" "$EXEC_PLIST" || return 1
  fi
  start_job "$LABEL" "$PLIST" || return 1
}

started=0
if [[ "${CRONOVA_START:-0}" == "1" ]]; then
  if start_service; then
    started=1
  else
    echo >&2
    echo "cronova installed but FAILED to start. Recent log ($LOG_DIR/service.log):" >&2
    tail -n 20 "$LOG_DIR/service.log" 2>/dev/null >&2 || true
    echo "  inspect:  sudo launchctl print system/$LABEL" >&2
    exit 1
  fi
fi

# --- summary ---------------------------------------------------------------
echo
# (any generated admin password was printed by `cronova init`; it is not stored.)
if [[ $started -eq 1 ]]; then
  echo "cronova is running."
  echo "  sudo launchctl print system/$LABEL      # status"
  echo "  sudo launchctl print system/$EXEC_LABEL # executor status"
  echo "  tail -f $LOG_DIR/service.log $LOG_DIR/executor.log"
else
  echo "cronova installed (not started). Start it with:"
  echo "  sudo launchctl bootstrap system $EXEC_PLIST"
  echo "  sudo launchctl bootstrap system $PLIST"
fi
echo "  reconfigure anytime:  sudo cronova init -config $CONF_DIR/cronova.yaml -env $CONF_DIR/cronova.env"
echo "                        (then: sudo launchctl kickstart -k system/$LABEL)"
echo
echo "Tasks run with the HOST's interpreters — install python3 / java / etc. and"
echo "make sure they're on the PATH in $PLIST (Homebrew dirs are already included)."
