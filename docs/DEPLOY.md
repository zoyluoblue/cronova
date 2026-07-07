# Deploying cronova (Linux & macOS)

cronova is a **scheduler**, not a runtime. It schedules DAGs and launches each
task as an OS subprocess that runs with the **host machine's own interpreters**
(`sh`, `python3`, `java`, `psql`, …) — the same model as Azkaban. So the
recommended deployment is a single static binary managed by the OS service
manager — **systemd** on Linux, **launchd** on macOS — running directly on the
box. There is no container image to build and no runtimes to bundle: the box's
own tooling does the work.

Both platforms install the same way (one-click `curl | sudo bash` below); they
differ only in the service manager and file layout — see
[Platform layout](#platform-layout).

> Why not Docker? A container only sees the interpreters baked into its image,
> not the host's. Containerising a polyglot subprocess scheduler therefore forces
> you to either bloat the image with every runtime, or lose access to the host
> tooling the tasks depend on. Native + systemd sidesteps both. (If you still
> want the scheduler containerised, run the standalone `cronova-executor` on the
> host and point the scheduler at it over gRPC — see the README.)

## Quick install (one-click)

On a fresh amd64/arm64 **Linux or macOS** box with nothing but `curl`:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

`bootstrap.sh` detects the OS and CPU architecture, downloads the matching
prebuilt release, verifies its SHA256, extracts it, and runs the platform
installer — `install.sh` (Linux/systemd) or `install-macos.sh` (macOS/launchd) —
which lays out the directories, installs the service, runs the **setup wizard**
(`cronova init`), and starts it. On Linux it also creates a dedicated `cronova`
system user; on macOS the service runs as the invoking (`sudo`) user so tasks
stay unprivileged.

When a terminal is attached — **even through `curl | sudo bash`**, via `/dev/tty`
— the wizard walks you through the settings, each with a default that Enter
accepts:

```
cronova setup — press Enter to accept the [default].

HTTP port [8090]:
Console reachable from:
  1) all interfaces (0.0.0.0) — reachable by server IP
  2) this machine only (127.0.0.1) — use a reverse proxy / SSH tunnel
choose [1]:
Admin username [admin]:
Admin password (blank = generate a strong one):   (input hidden)
Require login for the console/API (recommended) [Y/n]:
```

With no terminal (CI, `CRONOVA_NONINTERACTIVE=1`, or a plain pipe) it takes the
defaults and `CRONOVA_ADMIN_USER` / `CRONOVA_ADMIN_PASSWORD` from the env,
generating a random password if none is given. Either way the admin credentials
(including any generated password) are printed at the end.

Re-run it anytime to reconfigure:

```bash
sudo cronova init -config /etc/cronova/cronova.yaml -env /etc/cronova/cronova.env
sudo systemctl restart cronova
```

### Presetting config (non-interactive)

Env vars let you configure everything up front — ideal for CI or unattended
installs. With `CRONOVA_NONINTERACTIVE=1` (or a plain pipe with no TTY) the wizard
is skipped and these values are baked into `cronova.yaml` / `cronova.env`. Pass
them through the pipe with `sudo -E`:

```bash
curl -fsSL .../bootstrap.sh | \
  CRONOVA_NONINTERACTIVE=1 \
  CRONOVA_HTTP="127.0.0.1:9000" CRONOVA_AUTH=true CRONOVA_TICK=5s \
  CRONOVA_ADMIN_USER=ops CRONOVA_ADMIN_PASSWORD='s3cret' \
  sudo -E bash
```

| Env var | Default | Effect |
|---|---|---|
| `CRONOVA_VERSION` | `latest` | Which release to install. |
| `CRONOVA_START` | `1` | `0` = install but don't start. |
| `CRONOVA_NONINTERACTIVE` | `0` | `1` = skip the wizard even with a TTY. |
| `CRONOVA_BASE_URL` | GitHub | Download origin (private mirror / air-gapped). |
| `CRONOVA_ADMIN_USER` | `admin` | First admin username (→ `cronova.env`). |
| `CRONOVA_ADMIN_PASSWORD` | generated | First admin password (→ `cronova.env`). |
| `CRONOVA_HTTP` | `:8090` | Console/API listen addr. `:8090` = all interfaces; `127.0.0.1:8090` = local only. |
| `CRONOVA_AUTH` | `true` | Require login for the console/API. |
| `CRONOVA_SESSION_TTL` | `24h` | Login session lifetime. |
| `CRONOVA_SECURE_COOKIE` | `false` | Mark the session cookie `Secure` (set behind HTTPS). |
| `CRONOVA_TICK` | `2s` | Scheduler loop interval (lower = snappier + more CPU). |
| `CRONOVA_EXECUTOR` | in-process | gRPC executor target for crash-recovery (e.g. `unix:///run/cronova/executor.sock`). |

Storage paths (`db`/`dags`/`logs`) are **not** presettable this way — the service
unit/plist sets them via flags, which win. Change them by editing the unit/plist.

The rest of this doc covers the **from-source** path and the layout/PATH details
that both paths share.

## What each task type needs on the host

| Task `type` | Runs as | Host requirement |
|---|---|---|
| `shell`  | `sh -c "<command>"` | `/bin/sh` (always present) + whatever the command calls (`python`, `java -jar`, CLIs) |
| `python` | `python3 -c "<code>"` | `python3` (falls back to `python`) on `PATH` |
| `sql`    | Go DB driver, in-process | **nothing** — postgres/mysql/sqlite drivers are built into the binary |
| `http`   | Go HTTP client, in-process | **nothing** |

`sql` and `http` are self-contained in the binary. `shell` and `python` (and
anything a shell task shells out to, e.g. `java`) use the host's tools — install
them and make sure they are on the service `PATH` (see below).

## 1. Build the binary

On any machine with **Go 1.26+**:

```bash
make release          # -> dist/cronova   (static, no CGO, linux/amd64)
```

No Go on your build box? Produce the binary with a throwaway build container
(this is a *build* step — nothing Docker is needed at runtime):

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26 \
  sh -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/cronova ./cmd/cronova'
```

Copy `dist/cronova` (plus the `deploy/` dir, `cronova.yaml.example` and your
`dags/`) to the server.

## 2. Install as a systemd service

```bash
sudo ./deploy/install.sh          # uses dist/cronova or ./cronova
```

The installer is idempotent (re-run it to upgrade) and will:

- create the `cronova` system user,
- install the binary to `/usr/local/bin/cronova`,
- lay out `/etc/cronova` (config), `/var/lib/cronova/dags` (DAGs, writable so the
  console can edit them), `/var/log/cronova` (task logs),
- seed `cronova.yaml`, `cronova.env`, and the example DAGs (only if absent),
- install and load `cronova.service`.

## 3. Configure & start

```bash
sudoedit /etc/cronova/cronova.yaml     # set auth.enabled: true on a shared host
sudoedit /etc/cronova/cronova.env      # set CRONOVA_ADMIN_PASSWORD, chmod 600

sudo systemctl enable --now cronova
systemctl status cronova
journalctl -u cronova -f
```

Console + API: `http://<server>:8090`.

## Controlling the service

`cronova` wraps the platform service manager, so the same commands work on Linux
(systemd) and macOS (launchd) — no need to remember `systemctl`/`launchctl`:

```bash
cronova start           # start (and enable auto-start where applicable)
cronova stop            # stop
cronova restart         # restart (after editing config)
cronova status          # show status
cronova update          # upgrade to the latest release, then restart
cronova uninstall       # remove the service + binary
```

Mutating commands need root; they **auto-elevate via `sudo`** (prompting for a
password if needed), so you can drop the `sudo` prefix. Set `CRONOVA_NO_SUDO=1`
to disable that and manage privileges yourself. `status` is read-only and never
escalates.

`start`/`stop`/`restart` shell out to `systemctl <action> cronova` / `launchctl`
under the hood, so the native commands (below) still work if you prefer them.

### Updating

```bash
cronova update                              # latest release for this OS/arch, verified + swapped atomically
cronova update v0.2.0                        # a specific tag (re-install or downgrade)
cronova update -proxy http://127.0.0.1:7890  # download through a proxy
```

`update` downloads the prebuilt release from GitHub (same asset + `SHA256SUMS`
verification as the bootstrap installer), atomically replaces the installed
binary and refreshes the service definition (backing the old ones up, so a failed
restart — verified by confirming the service actually stays running — rolls back
automatically), and restarts the service. `CRONOVA_BASE_URL=<origin>` points it at
a private mirror; it **must be `https://`** (plain `http://` is allowed only for
`localhost`), and downgrade redirects are refused. `update` does **not** touch
your config, DB or DAGs.

**Behind a proxy** (e.g. a restricted network): `-proxy http://host:port` or
`-proxy socks5://host:port` routes the download through it (bare `host:port` means
an http proxy). It also honors the standard `CRONOVA_UPDATE_PROXY`, `HTTPS_PROXY`,
and `ALL_PROXY` env vars — and those survive the automatic `sudo` escalation.

### Uninstalling

```bash
cronova uninstall           # stop + remove the service and binary; KEEP data
cronova uninstall --purge   # also delete config, DB, DAGs, logs (+ the cronova user on Linux)
cronova uninstall -yes      # skip the confirmation prompt (for scripts)
```

Data lives outside the binary (`/usr/local/{etc,var}/cronova` on macOS,
`/etc/cronova` + `/var/lib/cronova` on Linux), so a plain `uninstall` is
reversible by re-installing. Only `--purge` deletes it.

## macOS (launchd)

The one-click installer above works on macOS too. To install from source instead
(needs **Go 1.26+**):

```bash
make build                       # -> ./cronova for the host (Apple Silicon/Intel)
sudo ./deploy/install-macos.sh   # installs a launchd LaunchDaemon + runs the wizard
```

`install-macos.sh` mirrors the Linux installer: it lays out `/usr/local/etc/cronova`
(config), `/usr/local/var/cronova` (DB + DAGs), `/usr/local/var/log/cronova`
(logs), renders `com.cronova.plist` into `/Library/LaunchDaemons`, and loads it.
The daemon runs as **the user who ran `sudo`** (not root), so tasks stay
unprivileged and the console can edit DAGs.

```bash
sudo launchctl print system/com.cronova          # status
tail -f /usr/local/var/log/cronova/service.log    # logs
sudo launchctl kickstart -k system/com.cronova    # restart (after editing config)
sudo launchctl bootout system/com.cronova         # stop + unload
```

`launchd` also hands a minimal `PATH`; the plist already includes the Homebrew
dirs (`/opt/homebrew/bin`, `/usr/local/bin`). If your tasks use tools elsewhere
(pyenv/conda Python, SDKMAN `java`, …), add those dirs to the `PATH` string in
`/Library/LaunchDaemons/com.cronova.plist` and `sudo launchctl kickstart -k
system/com.cronova`.

## PATH: the one gotcha

systemd gives services a **minimal `PATH`**. cronova's `shell`/`python` tasks
inherit it, so a tool your tasks rely on may be "not found" even though it works
in your interactive shell. The unit sets a sane default:

```
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
```

If your tasks use tools elsewhere (e.g. `/opt/tool/bin`, a pyenv/conda Python, or
SDKMAN's `java`), add those directories here via a drop-in:

```bash
sudo systemctl edit cronova     # writes /etc/systemd/system/cronova.service.d/override.conf
```

```ini
[Service]
Environment=PATH=/opt/tool/bin:/usr/local/bin:/usr/bin:/bin
```

## Platform layout

Same binary, same wizard; the service manager and paths differ:

| Purpose | Linux (systemd) | macOS (launchd) |
|---|---|---|
| binary | `/usr/local/bin/cronova` | `/usr/local/bin/cronova` |
| config | `/etc/cronova/cronova.yaml` | `/usr/local/etc/cronova/cronova.yaml` |
| secrets (admin seed, 0600) | `/etc/cronova/cronova.env` | `/usr/local/etc/cronova/cronova.env` |
| SQLite DB | `/var/lib/cronova/cronova.db` | `/usr/local/var/cronova/cronova.db` |
| DAG YAML (console-editable) | `/var/lib/cronova/dags/` | `/usr/local/var/cronova/dags/` |
| task logs | `/var/log/cronova/` | `/usr/local/var/log/cronova/` |
| service unit | `/etc/systemd/system/cronova.service` | `/Library/LaunchDaemons/com.cronova.plist` |
| runs as | `cronova` system user | the `sudo` user |
| control | `systemctl`, `journalctl` | `launchctl`, `service.log` |
| uploaded projects | `~cronova/.cronova/projects/` | `~/.cronova/projects/` (sudo user) |

## Uploaded projects

The console can upload scripts / project folders / zips (task editor →
**Project**); a shell task with `project: <name>` runs its command inside a
fresh temp copy of that directory (deleted when the attempt ends; leftovers from
crashes are garbage-collected). Constraints to know about:

- **Same-host only.** The scheduler stages the copy on ITS filesystem, so the
  executor must share it: the default in-process executor, or a gRPC executor on
  the same host / shared mount. A remote executor on another machine will not
  see the files.
- **Linux + standalone executor:** the workspace copies live under the temp dir.
  If you run `cronova-executor` as its own systemd unit with `PrivateTmp=true`,
  scheduler and executor get DIFFERENT /tmp namespaces and staging breaks — keep
  both in one unit, or disable PrivateTmp for the executor.
- **Size limits:** 10 MiB per file, 50 MiB per project (small script projects,
  not datasets). Re-uploads take effect on the NEXT run (live-latest).

## Sandbox

`cronova.service` ships a mild sandbox (`ProtectSystem=full`, `NoNewPrivileges`,
`PrivateTmp`) that protects system directories while leaving `/var`, `/opt`,
`/srv`, `/home` writable for tasks. Because tasks are arbitrary host commands,
tighten or loosen it to match your workload — e.g. `ProtectSystem=strict` plus an
explicit `ReadWritePaths=` list for a locked-down box, or drop `PrivateTmp=true`
if tasks must share `/tmp` with other processes.

## Upgrading

The one-liner — downloads the latest release, verifies it, swaps the binary
atomically (rolls back on a failed restart) and restarts the service:

```bash
cronova update            # or: cronova update v0.2.0 to pin a version
```

From source instead (needs Go 1.26; the installer is idempotent and keeps
config/DAGs/DB):

```bash
make release
sudo ./deploy/install.sh          # replaces the binary, keeps config/DAGs/DB
sudo systemctl restart cronova
```

In-process executor: a restart ends running tasks. For zero-loss restarts run the
standalone `cronova-executor` and point `serve` at it (`-executor`); see the
README's crash-recovery section.

## Cutting a release (maintainers)

The one-click installer pulls prebuilt binaries from GitHub Releases. To publish
a version, push a tag — `.github/workflows/release.yml` does the rest:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The workflow cross-compiles static binaries for **`linux` and `darwin`,
`amd64` and `arm64`** (pure Go, CGO off, so darwin cross-builds from Linux),
bundles each with `deploy/`, `cronova.yaml.example`, the example DAGs and
`docs/DEPLOY.md` into `cronova_<os>_<arch>.tar.gz`, generates `SHA256SUMS`, and
attaches all five files to the release. `bootstrap.sh` and `cronova update`
download from `releases/latest/download/` (or `releases/download/<tag>/` when
pinned).

Build the same artifacts locally:

```bash
make package          # -> dist/cronova_linux_{amd64,arm64}.tar.gz + SHA256SUMS
```

> Requires a **public** repo (or the target has a token) so `curl` can fetch
> `bootstrap.sh` from `raw.githubusercontent.com` and the release assets.

