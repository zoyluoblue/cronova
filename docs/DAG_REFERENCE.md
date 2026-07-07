# DAG & Task Reference

The complete YAML schema for a cronova **DAG** (directed acyclic graph of tasks) — every DAG-level and task-level field, the five task types, trigger rules, and resource pools. For a hands-on introduction see [Getting Started](GETTING_STARTED.md); for the project overview see the [README](https://github.com/zoyluoblue/cronova#readme).

A DAG is a single YAML file in the `dags/` directory (default `./dags`, or the service's data dir). cronova validates and cycle-checks every DAG on load; runnable examples live in [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags).

```yaml
dag_id: daily_etl
schedule: "0 2 * * *"
start_date: 2026-06-01
catchup: true
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
trigger_after:
  - dag_id: upstream_ingest
notify:
  - url: https://hooks.example.com/cronova
    on: [failure]
```

## DAG-level fields

| Field | Type | Default | Description |
|---|---|---|---|
| `dag_id` | string | — (required) | Unique identifier for the DAG. |
| `schedule` | string | `""` (manual) | Cron expression (`"0 2 * * *"`) **or** an interval (`"@every 30s"`). Empty = the DAG runs only on manual trigger or `trigger_after`. |
| `start_date` | date string | — | Earliest logical date the DAG is scheduled for; anchors catchup/backfill. |
| `catchup` | bool | `false` | Backfill missed periods between `start_date` and now. Backfilled runs are throttled so they never flood. |
| `max_active_runs` | int | `1` | Maximum concurrent runs of this DAG (0 is treated as 1). |
| `default_retries` | int | `0` | Retry count applied to tasks that don't set their own `retries`. |
| `default_retry_delay` | int (seconds) | `0` | Retry delay applied to tasks that don't set their own `retry_delay`. |
| `sla` | int (seconds) | `0` | Run-level soft deadline, measured from run start. A breach raises an alert; it does not cancel the run. |
| `dagrun_timeout` | int (seconds) | `0` | Run-level hard deadline, measured from run start. `0` = no limit. |
| `tasks` | list | — (required) | The task list (see below). |
| `trigger_after` | list of `{dag_id}` | — | Run this DAG after another DAG **succeeds** (cross-DAG dependency). Visualized in the console's DAG Graph. |
| `notify` | list of `{url, on}` | — | Webhook notifications. `on` is a list of `"failure"` and/or `"success"`. |

> `paused` is **not** a YAML field. Pausing is operational state managed from the console, CLI (`cronova pause <dag_id>`), or API, and is preserved across DAG reloads.

## Task-level fields

Each entry under `tasks:` describes one task.

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | — (required) | Task identifier, unique within the DAG. |
| `type` | string | `shell` | One of `shell`, `python`, `sql`, `jar`, `http`. See [Task types](#task-types). |
| `command` | string | — | The command (shell), code (python), or query (sql). Supports [template variables](#template-variables). Not used for `http`. |
| `deps` | list of task ids | — | Upstream tasks that must satisfy this task's `trigger_rule` before it runs. Edges are cycle-checked. |
| `pool` | string | `default` | The [resource pool](#resource-pools) this task consumes a slot from. |
| `priority` | int | `0` | Higher runs first when tasks contend for the same pool. |
| `retries` | int | inherits `default_retries` | Times to retry on failure. |
| `retry_delay` | int (seconds) | inherits `default_retry_delay` | Delay between retries. |
| `timeout` | int (seconds) | `0` | Per-attempt execution timeout; on breach the whole process group is killed. `0` = none. |
| `sla` | int (seconds) | `0` | Task soft deadline from run start; breach alerts only. |
| `trigger_rule` | string | `all_success` | When to run relative to upstream states. See [Trigger rules](#trigger-rules). |
| `conn` | string | — | Connection id for a `sql` task (selects driver + builds the DSN). |
| `project` | string | — | Name of an uploaded project directory to stage as the working directory (shell tasks). See [Getting Started → Projects](GETTING_STARTED.md). |
| `http` | object | — | HTTP request spec for `http` tasks (see below). |

### `http` task spec

Set under a task's `http:` key when `type: http`:

| Field | Type | Default | Description |
|---|---|---|---|
| `method` | string | `GET` | HTTP method. |
| `url` | string | — (required) | Request URL. Supports templates (e.g. `https://{{ conn.api.host }}/path`). |
| `headers` | map | — | Header name → value; values support templates (e.g. `Authorization: Bearer {{ var.TOKEN }}`). |
| `body` | string | — | Request body; supports templates. |
| `expected_status` | list of int | `2xx` | Status codes considered success (e.g. `[200, 201]`). |

## Task types

| Type | Runs as | `command` holds | Needs on host |
|---|---|---|---|
| `shell` | OS subprocess (`sh -c`) | any shell command | the tools the command invokes |
| `python` | OS subprocess (`python3`) | Python code | `python3` on the service `PATH` |
| `sql` | in-process (native driver) | the SQL query; `conn` selects the connection | nothing extra |
| `jar` | OS subprocess (`java`) | a `java -jar …` command | a JRE/JDK on the `PATH` |
| `http` | in-process HTTP client | — (use the `http:` spec) | nothing extra |

`sql` and `http` tasks are self-contained in the binary. `shell`, `python`, and `jar` tasks (and anything a shell task invokes) require that tool installed and on the **service** `PATH` — see [Deployment](DEPLOY.md).

```yaml
tasks:
  - id: shell_task
    type: shell
    command: "echo running {{ logical_date }}"
  - id: python_task
    type: python
    command: |
      import os
      print(os.environ['CRONOVA_LOGICAL_DATE'])
  - id: sql_task
    type: sql
    conn: warehouse
    command: "SELECT count(*) FROM events WHERE day = '{{ params.day }}'"
  - id: jar_task
    type: jar
    command: "java -jar app.jar --in {{ logical_date }}"
  - id: http_task
    type: http
    http:
      method: POST
      url: "https://{{ conn.api.host }}/ingest"
      headers: { Authorization: "Bearer {{ var.TOKEN }}" }
      body: '{"date":"{{ logical_date }}"}'
      expected_status: [200, 201]
```

## Template variables

Any `command`, `url`, header, `body`, or query can reference `{{ name }}` placeholders, substituted at dispatch. Built-in run variables are also injected into the process environment as `CRONOVA_<NAME>` (uppercased):

| Variable | Env var | Meaning |
|---|---|---|
| `{{ logical_date }}` | `CRONOVA_LOGICAL_DATE` | The run's logical date (`YYYY-MM-DD`) — the period it represents, which is what makes catchup meaningful. |
| `{{ logical_datetime }}` | `CRONOVA_LOGICAL_DATETIME` | Logical date-time, RFC3339. |
| `{{ run_id }}` | `CRONOVA_RUN_ID` | Unique id of this run. |
| `{{ dag_id }}` | `CRONOVA_DAG_ID` | The DAG id. |
| `{{ task_id }}` | `CRONOVA_TASK_ID` | This task's id. |
| `{{ try_number }}` | `CRONOVA_TRY_NUMBER` | Attempt number (increments on retry). |

Plus UI-managed references, resolved server-side (secrets never enter the blanket env):

- `{{ var.KEY }}` — a shared [variable](AGENTS.md).
- `{{ conn.ID.FIELD }}` — a connection field: `host`, `port`, `login` (alias `user`), `password`, `type`, or an extra JSON field as `extra.KEY`.
- `{{ params.KEY }}` — a manual-trigger parameter (also injected as `CRONOVA_PARAM_<KEY>`).

In the console task editor these are inserted as click/drag **pills** — you don't type the `{{ }}`.

## Trigger rules

`trigger_rule` decides when a task runs given its upstream (`deps`) task states:

| Rule | Runs when |
|---|---|
| `all_success` (default) | every upstream task succeeded |
| `all_done` | every upstream task finished (any state) |
| `all_failed` | every upstream task failed |
| `one_success` | at least one upstream task succeeded |
| `one_failed` | at least one upstream task failed |
| `none_failed` | no upstream task failed (success or skipped) |

## Resource pools

A **pool** is a named set of global concurrency slots; a task consumes one slot of its `pool` while running, and higher-`priority` tasks win contended slots. Pools are global resources configured out-of-band (not in DAG YAML):

```bash
cronova pools                    # list pools and usage
cronova pools set reports 4      # create/resize the "reports" pool to 4 slots
```

Every task defaults to the `default` pool. See the [CLI Reference](CLI.md#local-operations) and [Architecture](ARCHITECTURE.md).

## See also

- [Getting Started](GETTING_STARTED.md) · [CLI Reference](CLI.md) · [AI Agents (MCP)](AGENTS.md) · [Deployment](DEPLOY.md) · [Architecture](ARCHITECTURE.md) · [FAQ](FAQ.md)
