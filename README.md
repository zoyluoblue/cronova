# cronova

A workflow scheduler in the spirit of Airflow / Azkaban — single Go binary, embedded SQLite, polyglot tasks, crash-recoverable execution.

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
