# Deploying cronova (Linux & macOS)

cronova is a **scheduler**, not a runtime. It schedules DAGs and launches each
task as an OS subprocess that runs with the **host machine's own interpreters**
(`sh`, `python3`, `java`, `psql`, …) — the same model as Azkaban. So the
recommended deployment is two static binaries managed by the OS service manager:
the `cronova` scheduler and the `cronova-executor` process that owns running
tasks. They run under **systemd** on Linux or **launchd** on macOS. There is no
container image to build and no runtimes to bundle: the box's own tooling does
the work.

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

On an amd64/arm64 **Linux or macOS** box with standard `curl`, `tar`, and
`sha256sum`/`shasum` tooling:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

`bootstrap.sh` detects the OS and CPU architecture, downloads the matching
prebuilt release, verifies its SHA256, extracts it, and runs the platform
installer — `install.sh` (Linux/systemd) or `install-macos.sh` (macOS/launchd) —
which lays out the directories, installs both services, runs the **setup wizard**
(`cronova init`), and starts the executor before the scheduler. On Linux it also
creates a dedicated `cronova` system user; on macOS both services run as the
invoking (`sudo`) user so tasks stay unprivileged.

When a terminal is attached — **even through `curl | sudo bash`**, via `/dev/tty`
— the wizard walks you through the settings, each with a default that Enter
accepts:

```
cronova setup — press Enter to accept the [default].

HTTP port [8090]:
Console reachable from:
  1) all interfaces (0.0.0.0) — reachable by server IP
  2) this machine only (127.0.0.1) — use a reverse proxy / SSH tunnel
choose [2]:
Admin username [admin]:
Admin password (blank = generate a strong one):   (input hidden)
Require login for the console/API (recommended) [Y/n]:
```

With no terminal (CI, `CRONOVA_NONINTERACTIVE=1`, or a plain pipe) it takes the
defaults and `CRONOVA_ADMIN_USER` / `CRONOVA_ADMIN_PASSWORD` from the install
environment, generating a random password if none is given. `cronova init`
stores only the password hash in SQLite; the plaintext is never written to
`cronova.yaml` or `cronova.env`. Any generated password is printed once.

Re-run it anytime to reconfigure:

```bash
sudo cronova init -config /etc/cronova/cronova.yaml -env /etc/cronova/cronova.env
sudo systemctl restart cronova
```

### Presetting config (non-interactive)

Env vars let you configure everything up front — ideal for CI or unattended
installs. With `CRONOVA_NONINTERACTIVE=1` (or a plain pipe with no TTY) the wizard
is skipped; non-secret settings are written to `cronova.yaml`, while the admin
is seeded directly into SQLite. Pass the values through the pipe with `sudo -E`:

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
| `CRONOVA_ADMIN_USER` | `admin` | Admin username seeded directly into SQLite. |
| `CRONOVA_ADMIN_PASSWORD` | generated | Admin password used once by `init`; never persisted as plaintext. |
| `CRONOVA_HTTP` | `127.0.0.1:8090` | Console/API listen addr. `:8090` = all interfaces and requires auth. |
| `CRONOVA_AUTH` | `true` | Require login for the console/API. |
| `CRONOVA_SESSION_TTL` | `24h` | Login session lifetime. |
| `CRONOVA_SECURE_COOKIE` | `false` | Mark the session cookie `Secure` (set behind HTTPS). |
| `CRONOVA_TICK` | `2s` | Scheduler loop interval (lower = snappier + more CPU). |
| `CRONOVA_TASK_ENV_ALLOWLIST` | empty | Comma/space-separated parent env names shell tasks may inherit in addition to the safe built-ins. `CRONOVA_*` secrets are not inherited. |

Installed services always use the bundled standalone executor over a private
Unix socket. `CRONOVA_EXECUTOR` remains available for a manual `cronova serve`
process, but the managed unit/plist supplies its own socket path explicitly.

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

On any machine with **Go 1.26.5+**:

```bash
make release          # -> dist/cronova + dist/cronova-executor (linux/amd64)
```

No Go on your build box? Produce the binary with a throwaway build container
(this is a *build* step — nothing Docker is needed at runtime):

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26.5 \
  sh -c 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/cronova ./cmd/cronova &&
         CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/cronova-executor ./cmd/cronova-executor'
```

Copy `dist/cronova`, `dist/cronova-executor` (plus the `deploy/` dir,
`cronova.yaml.example` and your `dags/`) to the server.

## 2. Install as a systemd service

```bash
sudo ./deploy/install.sh          # uses dist/cronova or ./cronova
```

The installer is idempotent (re-run it to upgrade) and will:

- create the `cronova` system user,
- install both binaries under `/usr/local/bin`,
- lay out `/etc/cronova` (config), `/var/lib/cronova/dags` (DAGs, writable so the
  console can edit them), `/var/lib/cronova/projects` and `workspaces`, and
  `/var/log/cronova` (task logs),
- seed `cronova.yaml`, a credential-free `cronova.env` override template, and
  the example DAGs (only if absent),
- install `cronova-executor.service` and `cronova.service`.

## 3. Configure & start

```bash
sudoedit /etc/cronova/cronova.yaml     # set auth.enabled: true on a shared host
sudo cronova init -config /etc/cronova/cronova.yaml -env /etc/cronova/cronova.env

sudo systemctl enable --now cronova-executor cronova
systemctl status cronova-executor cronova
journalctl -u cronova-executor -u cronova -f
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

`start` starts the executor and then the scheduler; `stop` stops both. `restart`
restarts only the scheduler so an already-running executor can keep ownership of
in-flight tasks. The native commands below remain available when you need an
explicit full stop/start.

### Updating

```bash
cronova update                              # latest release for this OS/arch, verified + swapped atomically
cronova update v0.2.0                        # a specific tag (re-install or downgrade)
cronova update -proxy http://127.0.0.1:7890  # download through a proxy
```

`update` downloads the prebuilt release from GitHub, requires the asset to be
listed in `SHA256SUMS`, bounds both downloads, and verifies it before atomically
replacing both installed binaries. It restarts and health-checks the scheduler;
an already-running executor is not bounced, so in-flight tasks survive (the new
executor binary takes effect on the next full stop/start). A failed scheduler
restart rolls back automatically.

Managed unit/plist files are refreshed only while their hashes match the last
installer-managed versions. If you customized one, `update` preserves it and
writes the candidate as `*.dist` for manual review instead of overwriting local
hardening or `PATH` changes. `CRONOVA_BASE_URL=<origin>` points at a private
mirror; it **must be `https://`** (plain `http://` is allowed only for
`localhost`), and downgrade redirects are refused. `update` does **not** touch
your config, DB, DAGs, projects, or workspaces.

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
(needs **Go 1.26.5+**):

```bash
make build build-executor        # -> ./cronova + ./cronova-executor for this Mac
sudo ./deploy/install-macos.sh   # installs both LaunchDaemons + runs the wizard
```

`install-macos.sh` mirrors the Linux installer: it lays out `/usr/local/etc/cronova`
(config), `/usr/local/var/cronova` (DB, DAGs, projects, workspaces and private
executor socket), `/usr/local/var/log/cronova` (logs), and renders both
`com.cronova.plist` and `com.cronova.executor.plist` into
`/Library/LaunchDaemons`. Both daemons run as **the user who ran `sudo`** (not
root), so tasks stay unprivileged and the console can edit DAGs.

```bash
sudo launchctl print system/com.cronova          # status
sudo launchctl print system/com.cronova.executor # executor status
tail -f /usr/local/var/log/cronova/service.log /usr/local/var/log/cronova/executor.log
sudo launchctl kickstart -k system/com.cronova    # restart (after editing config)
sudo launchctl bootout system/com.cronova         # stop scheduler
sudo launchctl bootout system/com.cronova.executor # stop executor after tasks finish
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
| binaries | `/usr/local/bin/cronova`, `/usr/local/bin/cronova-executor` | `/usr/local/bin/cronova`, `/usr/local/bin/cronova-executor` |
| config | `/etc/cronova/cronova.yaml` | `/usr/local/etc/cronova/cronova.yaml` |
| optional env overrides (0600, no admin password) | `/etc/cronova/cronova.env` | `/usr/local/etc/cronova/cronova.env` |
| SQLite DB | `/var/lib/cronova/cronova.db` | `/usr/local/var/cronova/cronova.db` |
| DAG YAML (console-editable) | `/var/lib/cronova/dags/` | `/usr/local/var/cronova/dags/` |
| task logs | `/var/log/cronova/` | `/usr/local/var/log/cronova/` |
| service definitions | `/etc/systemd/system/cronova{,-executor}.service` | `/Library/LaunchDaemons/com.cronova{,.executor}.plist` |
| runs as | `cronova` system user | the `sudo` user |
| control | `systemctl`, `journalctl` | `launchctl`, `service.log` |
| uploaded projects | `/var/lib/cronova/projects/` | `/usr/local/var/cronova/projects/` |
| attempt workspaces | `/var/lib/cronova/workspaces/` | `/usr/local/var/cronova/workspaces/` |

**Back up `cronova.key` alongside the DB.** Connection passwords are encrypted
at rest (AES-256-GCM) with a key that `cronova serve` auto-generates on first
start as `cronova.key` (permissions `0600`) in its working directory —
`/var/lib/cronova/cronova.key` on Linux, `/usr/local/var/cronova/cronova.key`
on macOS (override with `key_file:` in `cronova.yaml` or `CRONOVA_KEY_FILE`).
Include it in the same backup as the SQLite DB: a database restored without its
key file has unreadable connection passwords, and they must be re-entered.

## Uploaded projects

The console can upload scripts / project folders / zips (task editor →
**Project**); a shell task with `project: <name>` runs its command inside a
fresh temp copy of that directory (deleted when the attempt ends; leftovers from
crashes are garbage-collected). Constraints to know about:

- **Same-host only.** The scheduler stages a per-attempt copy in the configured
  `workspaces` directory and the executor runs it there. The managed services
  use an explicit shared state path, so separate `PrivateTmp` namespaces do not
  break staging. TCP executor targets are deliberately unsupported.
- **Size limits:** 10 MiB per file, 50 MiB per project (small script projects,
  not datasets). Uploads are built in a staging tree and swapped atomically, so
  a failed multi-file upload cannot expose a mixed old/new project. Re-uploads
  take effect on the next run.

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

From source instead (needs Go 1.26.5+; the installer is idempotent and keeps
config/DAGs/DB):

```bash
make release
sudo ./deploy/install.sh          # replaces the binary, keeps config/DAGs/DB
sudo systemctl restart cronova
```

Managed installs already run the standalone executor, so a normal scheduler
restart or upgrade keeps running tasks alive. A manual `cronova serve` with an
empty `-executor` still uses the in-process mode and loses active tasks when the
process exits.

The standalone executor accepts only an absolute Unix socket. Put it in a
private (`0700`) directory; the executor forces the socket itself to `0600` and
rejects TCP targets. The default is `/tmp/cronova-<uid>/executor.sock`. For a
systemd pair, prefer a private `/run/cronova/` directory shared by both units.

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
`docs/DEPLOY.md` into `cronova_<os>_<arch>.tar.gz`, and generates `SHA256SUMS`.
Before publishing it runs the race-enabled test suite, vet, a 55% coverage
floor, `govulncheck`, and shell syntax checks. The release also includes an SPDX
SBOM and a GitHub artifact attestation for the checksum manifest. `bootstrap.sh`
and `cronova update` download from `releases/latest/download/` (or
`releases/download/<tag>/` when pinned).

Build the same artifacts locally:

```bash
make package          # -> dist/cronova_linux_{amd64,arm64}.tar.gz + SHA256SUMS
```

> Requires a **public** repo (or the target has a token) so `curl` can fetch
> `bootstrap.sh` from `raw.githubusercontent.com` and the release assets.
