# cronova

[![Release](https://img.shields.io/github/v/release/zoyluoblue/cronova?sort=semver&logo=github)](https://github.com/zoyluoblue/cronova/releases/latest)
[![Platform](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-informational)](docs/DEPLOY.md)
[![Go](https://img.shields.io/github/go-mod/go-version/zoyluoblue/cronova)](go.mod)

A workflow scheduler in the spirit of Airflow / Azkaban — single Go binary, embedded SQLite, polyglot tasks, crash-recoverable execution.

**Install on any Linux or macOS box in one line** (details: [docs/DEPLOY.md](docs/DEPLOY.md)):

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

cronova runs **DAGs** of tasks: each task is launched as an OS subprocess, so a task can be written in any language (Python, SQL via a CLI, a JAR, a shell one-liner, …). The framework language (Go) is fully decoupled from the task language.

> Design rationale and diagrams: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Features

- **DAGs as YAML** — declarative definitions with dependency edges, validated and cycle-checked.
- **Triggers** — cron / `@every` schedules, manual, cross-DAG dependency, (events: planned).
- **Decoupled executor over gRPC** — tasks run in a long-lived `cronova-executor` process, so restarting the scheduler never kills running tasks.
- **Crash recovery** — on restart the scheduler re-attaches to in-flight tasks by probing them; finished/lost tasks are reconciled. No double execution.
- **Retries & timeouts** — per-task retries with delay; timeouts kill the whole process group.
- **Resource pools** — global concurrency slots with priority ordering.
- **Catchup / backfill** — `logical_date` per run; missed periods are backfilled, throttled so they never flood.
- **Web console** — REST API + live log tailing (SSE) + embedded UI, served in-process.
- **Single binary, no CGO** — pure-Go SQLite (`modernc.org/sqlite`).

## Quickstart

```bash
go build -o cronova ./cmd/cronova

# start the scheduler + console (in-process executor)
./cronova serve            # console at http://localhost:8090

# in another terminal
./cronova dags                        # list DAGs from ./dags
./cronova trigger example_etl         # run a DAG now
./cronova runs example_etl            # see run history + task states
./cronova pools set reports 4         # size a resource pool
```

Open `http://localhost:8090` for the web console (DAG list, run history, task states, live logs, manual trigger).

### Crash-recoverable mode (separate executor)

```bash
# terminal 1 — long-lived executor
go build -o cronova-executor ./cmd/cronova-executor
./cronova-executor -sock /tmp/cronova-executor.sock

# terminal 2 — scheduler talks to it over gRPC
./cronova serve -executor unix:///tmp/cronova-executor.sock
```

Now `kill -9` the scheduler mid-run and restart it: running tasks survive in the executor and are re-attached on recovery.

## Deploy on a server (systemd)

cronova is a **scheduler, not a runtime**: it launches each task as a subprocess
that runs with the **host's own interpreters** (`sh`, `python3`, `java`, `psql`,
…), Azkaban-style. So it deploys as a single static binary under systemd — no
container, no bundled runtimes.

**One-click install** — downloads a prebuilt binary for your OS + CPU, sets up
the native service (systemd on Linux, launchd on macOS), and runs an interactive
setup wizard (port, bind scope, admin account, auth — each with a sensible
default; Enter accepts). Works even through the pipe:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

Non-interactive (CI, or to accept all defaults) — add `CRONOVA_NONINTERACTIVE=1`.

Prefer to inspect first? Download, read, then run:

```bash
curl -fsSLO https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh
less bootstrap.sh && sudo bash bootstrap.sh
```

Pin a version or preset the admin with env vars:
`CRONOVA_VERSION=v0.1.0 CRONOVA_ADMIN_PASSWORD=… sudo -E bash bootstrap.sh`.

**From source** (needs Go 1.26; or use the throwaway build container in
[docs/DEPLOY.md](docs/DEPLOY.md)):

```bash
make install              # build for the host + install the native service (needs sudo)
# Linux:  systemd unit + `cronova` system user   ·   macOS: launchd LaunchDaemon
```

Once installed, `cronova` manages its own lifecycle the same way on both
platforms (wraps systemd/launchd; mutating commands auto-elevate via `sudo`):

```bash
cronova start | stop | restart     # control the service   ·   cronova status  to check
cronova update                     # fetch + install the latest release, then restart
cronova update v0.2.0              # pin a specific version (re-install / downgrade)
cronova uninstall                  # remove the service + binary (keeps your data)
cronova uninstall --purge          # ...and delete config / DB / DAGs / logs too
```

`sql` and `http` tasks are self-contained in the binary; `shell` and `python`
tasks (and anything a shell task invokes, e.g. `java -jar`) need that tool
installed on the host and on the service `PATH`. Full guide, layout, and the
service `PATH` gotcha: **[docs/DEPLOY.md](docs/DEPLOY.md)**.

## Defining a DAG

Drop a YAML file in `./dags/` (see [dags/](dags/) for examples):

```yaml
dag_id: daily_etl
schedule: "0 2 * * *"        # cron; or "@every 30s"; omit for manual-only
start_date: 2026-06-01
catchup: true                # backfill missed periods
max_active_runs: 1
default_retries: 2
tasks:
  - id: extract
    type: shell
    command: "python extract.py --date {{ logical_date }}"
    pool: default
  - id: transform
    command: "python transform.py --date {{ logical_date }}"
    deps: [extract]
  - id: load
    command: "psql -f load.sql"
    deps: [transform]
    retries: 3
    timeout: 1800
trigger_after:               # optional: run after another DAG succeeds
  - dag_id: upstream_ingest
```

Template variables in `command`: `{{ logical_date }}`, `{{ logical_datetime }}`, `{{ run_id }}`, `{{ task_id }}`, `{{ try_number }}`. The same values are injected as `CRONOVA_*` environment variables.

### Running your own scripts / projects

Upload a script, a whole project folder, or a zip in the console (task editor →
**Project**), or drop files under the projects dir (default `~/.cronova/projects/<name>/`).
Then point a shell task at it:

```yaml
tasks:
  - id: run_main
    type: shell
    command: python3 main.py        # runs with cwd = a clean copy of the project
    project: my_app                 # a directory under the projects dir
```

Each attempt gets a **fresh temp copy** of the project as its working directory
(`CRONOVA_PROJECT_DIR` points there too), so runs are isolated and re-uploads
take effect on the next run. The copy is deleted when the attempt ends — write
durable outputs to stdout (the task log) or an external system, not the cwd.
Requires the scheduler and executor to share a filesystem (the default
in-process executor, or a same-host gRPC executor).

## CLI

| Command | Description |
|---|---|
| `cronova serve [-db -dags -logs -tick -executor -http]` | Run the scheduling loop + console |
| `cronova trigger <dag_id>` | Create a manual run |
| `cronova dags` | List registered DAGs |
| `cronova runs <dag_id> [-n N]` | Show recent runs and task states |
| `cronova pools [set <name> <slots>]` | List or resize resource pools |
| `cronova-executor -sock <path>` | Run the standalone gRPC executor |

## HTTP API

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/dags` | List DAGs |
| `GET` | `/api/dags/{id}` | DAG detail (parsed tasks + deps) |
| `POST` | `/api/dags/{id}/trigger` | Manual trigger |
| `POST` | `/api/dags/{id}/pause?paused=true` | Pause/resume |
| `GET` | `/api/dags/{id}/runs?limit=N` | Run history |
| `GET` | `/api/runs/{run_id}` | Run detail + task instances |
| `GET` | `/api/tasks/{ti_id}/log` | Task log |
| `GET` | `/api/tasks/{ti_id}/log/stream` | Live log tail (SSE) |
| `GET` | `/api/pools` · `POST /api/pools/{name}?slots=N` | Pools |

## Project layout

```
cmd/cronova            scheduler + console entrypoint (serve/trigger/dags/runs/pools)
cmd/cronova-executor   standalone gRPC task executor
internal/model         domain types + state machine
internal/store         persistence interface + sqlite implementation
internal/scheduler     scheduling loop, DAG parser, recovery
internal/executor      subprocess runner, gRPC server/client, local executor
internal/api           REST + SSE HTTP handlers
internal/web           embedded web console (static assets)
proto/                 gRPC service definitions (buf-generated)
dags/                  example DAG definitions
docs/ARCHITECTURE.md   design document
```

## Development

```bash
go test -race ./...      # full test suite

# regenerate gRPC code after editing proto/ (needs: go install buf + protoc-gen-go[-grpc])
buf generate
```

Notes:
- SQLite runs in **DELETE** journal mode with `MaxOpenConns(1)`: the pure-Go driver does not share WAL across processes, and the CLI accesses the same DB as a running `serve`.
- Pools are global resources, configured via `cronova pools set`, not per-DAG YAML.
