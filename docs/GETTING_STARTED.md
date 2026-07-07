# Getting Started with cronova

Install cronova, start the scheduler and web console, write and trigger your first DAG, and wire your own scripts into a workflow — the hands-on path for the single-binary, self-hosted **workflow scheduler** and open-source [Apache Airflow](https://airflow.apache.org/) / Azkaban alternative.

This guide is task-oriented. For the full field-by-field DAG spec see the [DAG Reference](DAG_REFERENCE.md); for every command and flag see the [CLI Reference](CLI.md); for production install see [Deployment](DEPLOY.md). New to cronova? Start with the [README](../README.md).

## 1. Install cronova

There are three ways to get the `cronova` binary. Pick the one that fits.

### Build from source

Build the scheduler, web console, and CLI into one static binary with Go 1.26+:

```bash
git clone https://github.com/zoyluoblue/cronova
cd cronova
go build -o cronova ./cmd/cronova
```

A plain `go build` reports its version as `dev`. The binary is CGO-free (pure-Go `modernc.org/sqlite`), so no C toolchain is required.

### One-line installer (Linux / macOS)

The installer downloads the matching prebuilt release, verifies its SHA256, installs the native service (systemd on Linux, launchd on macOS), and runs an interactive setup wizard (port, bind scope, admin account, auth):

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

This is the recommended path for a real deployment. Full production guide: [Deployment](DEPLOY.md).

### Prebuilt release

Grab a binary for your OS/arch (Linux and macOS, amd64 and arm64) from the [Releases](https://github.com/zoyluoblue/cronova/releases) page, then make it executable:

```bash
chmod +x cronova
./cronova version        # prints: cronova <version> <os>/<arch>
```

## 2. Start the scheduler and open the console

`cronova serve` runs the scheduling loop **and** the web console + REST API in one process:

```bash
./cronova serve
```

Then open **http://localhost:8090** for the console — the DAG list, run history, task states, live log tailing, and one-click manual triggers.

By default `serve` uses relative working-directory paths and the in-process executor:

| Setting | Flag | Env var | Default |
|---|---|---|---|
| Console + API address | `-http` | `CRONOVA_HTTP` | `:8090` (all interfaces) |
| DAG YAML directory | `-dags` | `CRONOVA_DAGS` | `dags` |
| SQLite database | `-db` | `CRONOVA_DB` | `data/cronova.db` |
| Task log directory | `-logs` | `CRONOVA_LOGS` | `logs` |
| Uploaded projects | `-projects` | `CRONOVA_PROJECTS` | `~/.cronova/projects` |
| Scheduler tick | `-tick` | `CRONOVA_TICK` | `2s` |
| gRPC executor target | `-executor` | `CRONOVA_EXECUTOR` | *(empty = in-process)* |
| Require login | `-auth` | `CRONOVA_AUTH` | *(off unless configured)* |

Settings resolve in this precedence, highest first: **explicit flag → `CRONOVA_*` env → `cronova.yaml` config file → built-in default**. The config file is optional; `serve` only errors on a missing config if you pass `-config` explicitly.

> The default in-process executor runs tasks inside the `serve` process, so a restart ends running tasks. For crash-recoverable execution that survives a scheduler restart or upgrade, run tasks in the decoupled gRPC executor via `-executor` / `CRONOVA_EXECUTOR` — see [Deployment](DEPLOY.md).

Drive the same server from another terminal with the CLI:

```bash
./cronova dags                  # list DAGs loaded from ./dags
./cronova trigger example_etl   # create a manual run
./cronova runs example_etl      # run history + per-task states
```

### Enabling login

Authentication is off unless you turn it on. A fresh `serve` with auth disabled prints a warning, because anyone who can reach the console can trigger or delete DAGs. Enable it and seed an admin:

```bash
CRONOVA_ADMIN_USER=admin CRONOVA_ADMIN_PASSWORD=... ./cronova serve -auth
```

The admin is created idempotently on first start. You can also manage accounts with `cronova users add|list|passwd|delete`, or let the `cronova init` wizard write `cronova.yaml` + a `0600` secrets file for you. Details in the [CLI Reference](CLI.md).

## 3. Write your first DAG and trigger it

A **DAG** (directed acyclic graph) is a set of tasks with dependency edges, defined as a YAML file in the `./dags/` directory. Each task runs as an OS subprocess. Create `dags/hello.yaml`:

```yaml
dag_id: hello
schedule: "@every 1m"        # cron ("0 2 * * *") or interval ("@every 30s"); omit for manual-only
start_date: 2026-06-01
catchup: false               # true = backfill missed periods from start_date
max_active_runs: 1
default_retries: 0
tasks:
  - id: greet
    type: shell
    command: echo "hello from cronova at $CRONOVA_LOGICAL_DATETIME"
  - id: report
    type: shell
    command: echo "run $CRONOVA_RUN_ID finished greeting"
    deps: [greet]            # runs after greet succeeds
```

The scheduler loads `*.yaml` and `*.yml` files from the DAG directory. A malformed file is logged and skipped, not fatal. See the runnable examples in [`dags/`](../dags/) — `example_etl.yaml`, `ticker.yaml`, `upstream_ingest.yaml`, and `downstream_report.yaml`.

Now run it. With `serve` running, list and trigger the DAG:

```bash
./cronova dags                # hello appears with SCHEDULE=@every 1m
./cronova trigger hello       # create a manual run — serve picks it up next tick
./cronova runs hello          # watch task states advance: greet, then report
```

`cronova trigger` only creates the run row; a running `cronova serve` executes it on the next tick (default every `2s`). You can also trigger from the console with one click, or pass trigger params as JSON:

```bash
./cronova trigger hello -params '{"day":"2026-01-01"}'
```

Common task fields — `type` (`shell`, `python`, `sql`, `jar`, `http`), `command`, `deps`, `pool`, `retries`, `retry_delay`, `timeout`, `trigger_rule`, `project` — and DAG-level fields like `schedule`, `catchup`, `max_active_runs`, `default_retries`, `trigger_after`, and `dagrun_timeout` are documented in full in the [DAG Reference](DAG_REFERENCE.md).

## 4. Template variables

Any task `command` (and an `http` task's URL, headers, and body) can reference template variables with `{{ … }}`. The scheduler substitutes them per task instance; unknown placeholders are left untouched so ordinary shell braces are not mangled.

### Built-in run variables

These are always available, and the same values are also injected as `CRONOVA_*` environment variables:

| Template | Env var | Value |
|---|---|---|
| `{{ run_id }}` | `CRONOVA_RUN_ID` | Unique id of this run |
| `{{ dag_id }}` | `CRONOVA_DAG_ID` | The DAG id |
| `{{ task_id }}` | `CRONOVA_TASK_ID` | The task id |
| `{{ try_number }}` | `CRONOVA_TRY_NUMBER` | Attempt number (increments on retry) |
| `{{ logical_date }}` | `CRONOVA_LOGICAL_DATE` | The run's logical date, `YYYY-MM-DD` |
| `{{ logical_datetime }}` | `CRONOVA_LOGICAL_DATETIME` | The logical date as RFC 3339 |

The **logical date** is what makes catchup meaningful: a backfilled run processes the data for the period it represents, not wall-clock "now". Use either form:

```yaml
tasks:
  - id: extract
    type: shell
    command: python extract.py --date {{ logical_date }}     # via template
  - id: load
    type: shell
    command: echo "loading for $CRONOVA_LOGICAL_DATE"        # via env var
    deps: [extract]
```

### Trigger params, variables, and connections

Three more namespaces resolve lazily from state you manage in the console (or via the API):

- `{{ params.KEY }}` — a per-run trigger param (from `cronova trigger -params '{...}'` or the console). Each param is also exported as `CRONOVA_PARAM_<KEY>` (uppercased).
- `{{ var.KEY }}` — a UI-managed **variable**. Fetched from the store only when referenced.
- `{{ conn.ID.field }}` — a field of a UI-managed **connection**. Valid fields are `host`, `port`, `login` (alias `user`), `password`, `type`, and any extra JSON field as `extra.KEY`.

```yaml
tasks:
  - id: notify
    type: shell
    command: curl -u {{ conn.api.login }}:{{ conn.api.password }} {{ var.webhook_url }}?day={{ params.day }}
```

> Variables and connections are **not** blanket-injected into every task's environment — they enter only through explicit `{{ var.X }}` / `{{ conn.Y.Z }}` references, so secrets don't leak into unrelated tasks' env. Only `run` variables and `params` become `CRONOVA_*` env vars.

In the console you don't type the `{{ }}` braces: the visual task editor renders each variable as a color-coded pill, and a grouped palette (built-in, variables, connections, params) inserts them by click or drag.

## 5. Run your own scripts and projects

To run a real script or a whole codebase, upload it as a **project** and point a shell task at it. cronova stages a fresh, isolated copy of the project as each attempt's working directory.

Upload from the console (task editor → **Project**). You can upload a single script, a whole folder, or a `.zip` (auto-extracted). Then reference the project by name:

```yaml
tasks:
  - id: run_main
    type: shell
    command: python3 main.py     # cwd is a clean copy of the project, so this resolves
    project: my_app
```

How project attach works:

- The `project` field is honored for **shell** tasks only (the `python`/`sql`/`http` task types run in-process, where a working directory is meaningless).
- Each attempt gets a **fresh isolated copy** of the uploaded project as its `cwd`. Attempts never interfere, and a re-upload takes effect on the next run.
- The copy's path is exported as **`CRONOVA_PROJECT_DIR`**, so a script can locate its own bundled data files.
- Project names allow letters, digits, and `. _ -`. Uploads are size-capped (per file and per project) and guarded against path traversal / zip-slip.
- The projects directory defaults to `~/.cronova/projects`; override it with `-projects` / `CRONOVA_PROJECTS`. If no projects directory is configured, uploads are disabled and a task that references a project will fail at run time — `cronova api POST /api/dags/validate` surfaces a warning for a project that isn't uploaded yet.

### Common questions

**Where does `python3 main.py` run from?**
From a clean per-attempt copy of the uploaded project, so a relative path like `main.py` or `./main.py` resolves. The absolute path to that copy is also in `CRONOVA_PROJECT_DIR`.

**Do I have to re-upload after editing my script?**
Re-upload the changed file (uploads are additive/upsert). Because each attempt copies the current project, the next run picks up your change — running attempts keep the copy they started with.

**Which languages can a task use?**
Any on the host. A `shell` task can invoke Python, Node, Go/Rust binaries, `psql`, a JAR — anything installed. The scheduler is fully decoupled from the task language.

**Why did my project task fail immediately?**
Most often the project isn't uploaded, or the server has no projects directory configured. Validate the DAG first; the response flags a `project` that references something missing.

## 6. Next steps

You now have a running scheduler, a first DAG, template variables, and a project-backed task. Where to go next:

- [DAG Reference](DAG_REFERENCE.md) — every DAG and task field, all task types (`shell`, `python`, `sql`, `jar`, `http`), trigger rules, cross-DAG `trigger_after`, retries, timeouts, and resource pools.
- [CLI Reference](CLI.md) — every `cronova` command and flag: `serve`, `trigger`, `dags`, `runs`, `pools`, `users`, `init`, and the remote/agent verbs.
- [Deployment](DEPLOY.md) — install as a systemd/launchd service, the crash-recoverable gRPC executor, updates, and the service-`PATH` gotcha.
- [AI Agents (MCP)](AGENTS.md) — let AI agents list, create, validate, and trigger DAGs through the built-in MCP server and remote JSON CLI.
- [Architecture](ARCHITECTURE.md) — the execution model and design rationale.
- [FAQ](FAQ.md) — common questions, answered.
