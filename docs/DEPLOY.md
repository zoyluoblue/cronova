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
