# Deploying cronova on Linux

cronova is a **scheduler**, not a runtime. It schedules DAGs and launches each
task as an OS subprocess that runs with the **host machine's own interpreters**
(`sh`, `python3`, `java`, `psql`, …) — the same model as Azkaban. So the
recommended deployment is a single static binary managed by **systemd**, running
directly on the server. There is no container image to build and no runtimes to
bundle: the box's own tooling does the work.

> Why not Docker? A container only sees the interpreters baked into its image,
> not the host's. Containerising a polyglot subprocess scheduler therefore forces
> you to either bloat the image with every runtime, or lose access to the host
> tooling the tasks depend on. Native + systemd sidesteps both. (If you still
> want the scheduler containerised, run the standalone `cronova-executor` on the
> host and point the scheduler at it over gRPC — see the README.)

## Quick install (one-click)

On a fresh amd64/arm64 Linux box with nothing but `curl`:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

`bootstrap.sh` detects the CPU architecture, downloads the matching prebuilt
release, verifies its SHA256, extracts it, and runs `install.sh` — which creates
the service user, lays out the directories, installs the systemd unit, runs the
**setup wizard** (`cronova init`), and starts the service.

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

Knobs (all optional): `CRONOVA_VERSION` (default `latest`), `CRONOVA_ADMIN_USER`,
`CRONOVA_ADMIN_PASSWORD` (default: generated), `CRONOVA_START=0` to install
without starting. With env vars, use `sudo -E`:

```bash
CRONOVA_VERSION=v0.1.0 CRONOVA_ADMIN_PASSWORD='s3cret' curl -fsSL .../bootstrap.sh | sudo -E bash
```

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

## Layout reference

| Path | Purpose | Perms |
|---|---|---|
| `/usr/local/bin/cronova` | binary | 0755 |
| `/etc/cronova/cronova.yaml` | config (read-only at runtime) | 0644 |
| `/etc/cronova/cronova.env` | secrets (admin seed) | 0600 |
| `/var/lib/cronova/cronova.db` | SQLite metadata | 0750 dir |
| `/var/lib/cronova/dags/` | DAG YAML (console-editable) | 0750 dir |
| `/var/log/cronova/` | one log file per task try | 0750 dir |

## Sandbox

`cronova.service` ships a mild sandbox (`ProtectSystem=full`, `NoNewPrivileges`,
`PrivateTmp`) that protects system directories while leaving `/var`, `/opt`,
`/srv`, `/home` writable for tasks. Because tasks are arbitrary host commands,
tighten or loosen it to match your workload — e.g. `ProtectSystem=strict` plus an
explicit `ReadWritePaths=` list for a locked-down box, or drop `PrivateTmp=true`
if tasks must share `/tmp` with other processes.

## Upgrading

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

The workflow cross-compiles static `linux/amd64` and `linux/arm64` binaries,
bundles each with `deploy/`, `cronova.yaml.example`, the example DAGs and
`docs/DEPLOY.md` into `cronova_linux_<arch>.tar.gz`, generates `SHA256SUMS`, and
attaches all three to the release. `bootstrap.sh` downloads from
`releases/latest/download/` (or `releases/download/<tag>/` when pinned).

Build the same artifacts locally:

```bash
make package          # -> dist/cronova_linux_{amd64,arm64}.tar.gz + SHA256SUMS
```

> Requires a **public** repo (or the target has a token) so `curl` can fetch
> `bootstrap.sh` from `raw.githubusercontent.com` and the release assets.

