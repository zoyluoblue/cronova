# DAG & Task Reference

The complete YAML schema for a cronova **DAG** (directed acyclic graph of tasks) — every DAG-level and task-level field, the five task types, trigger rules, and resource pools. For a hands-on introduction see [Getting Started](GETTING_STARTED.md); for the project overview see the [README](https://github.com/zoyluoblue/cronova#readme).

A DAG is a single YAML file in the `dags/` directory (default `./dags`, or the service's data dir). cronova validates and cycle-checks every DAG on load; runnable examples live in [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags). Parsing is strict: unknown fields, unsupported task types, trailing YAML documents, invalid negative values, and out-of-range settings are rejected instead of ignored.

Safety limits are enforced before a definition can enter the scheduler: 1 MiB
per YAML document, 1,000 tasks, 10,000 dependency edges, 256 dependencies per
task, 128-byte identifiers, 256 KiB commands, 100 retries, and one year for any
configured delay/timeout/SLA. These bounds prevent a typo or oversized upload
from turning validation into unbounded memory or scheduler work.

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
  url: https://hooks.example.com/cronova
  on: [failure]
```

## DAG-level fields

| Field | Type | Default | Description |
|---|---|---|---|
| `dag_id` | string | — (required) | Unique identifier for the DAG. |
| `schedule` | string | `""` (manual) | Cron expression (`"0 2 * * *"`) **or** an interval (`"@every 30s"`). Empty = the DAG runs only on manual trigger, `trigger_after`, or `trigger_on_event`. |
| `timezone` | string | `""` (UTC) | IANA zone (e.g. `Asia/Shanghai`) the cron fields — and a date-only `start_date` — are evaluated in, DST included. |
| `start_date` | date string | — | Earliest logical date the DAG is scheduled for; anchors catchup/backfill. |
| `catchup` | bool | `false` | Backfill missed periods between `start_date` and now. Backfilled runs are throttled so they never flood. |
| `max_active_runs` | int | `1` | Maximum concurrent runs of this DAG (0 is treated as 1). |
| `max_active_tasks` | int | `0` (unlimited) | Cap on this DAG's concurrently queued/running **tasks** across all of its runs — a per-DAG budget complementing the global pools. |
| `execution_policy` | string | `parallel` | How queued runs are admitted when another run of this DAG is active: `parallel` (up to `max_active_runs` concurrently), `serial_wait` (one at a time; later runs queue in logical-date order), `serial_discard` (one at a time; runs arriving while busy are **cancelled**, visibly), or `serial_priority` (one at a time; the queue drains highest run priority first — set priority when triggering). Serial policies force at most **one** active run regardless of `max_active_runs`. |
| `worker_group` | string | `""` (local) | Default dial-in [worker](#task-level-fields) group for every task that doesn't set its own `worker_group`. Empty = tasks run on the scheduler's configured local executor. |
| `trigger_on_event` | list of strings | — | External event keys this DAG subscribes to: `POST /api/events {"key": …}` creates one event-triggered run per subscriber (idempotent per key). The event payload becomes the run's params. |
| `default_retries` | int | `0` | Retry count applied to tasks that don't set their own `retries`. |
| `default_retry_delay` | int (seconds) | `0` | Retry delay applied to tasks that don't set their own `retry_delay`. |
| `sla` | int (seconds) | `0` | Run-level soft deadline, measured from run start. A breach raises an alert; it does not cancel the run. |
| `dagrun_timeout` | int (seconds) | `0` | Run-level hard deadline, measured from run start. `0` = no limit. |
| `tasks` | list | — (required) | The task list (see below). |
| `trigger_after` | list of `{dag_id}` | — | Run this DAG after another DAG **succeeds** (cross-DAG dependency). Visualized in the console's DAG Graph. |
| `notify` | `{url, on, format, group}` | — | Run-completion notification. `on` is a list of `"failure"` and/or `"success"`. `url` is an `http(s)://` webhook **or** `mailto:addr[,addr]` (delivered through the server's `smtp:` relay). |
| `notify.format` | string | `raw` | One of `raw`, `slack`, `feishu`, `dingtalk`, `email`. `raw` posts the full JSON payload; the chat formats wrap the summary text in the platform's incoming-webhook envelope, so the message renders in Slack/Feishu/DingTalk without a relay service; `email` is the plain-text mail body used for `mailto:` targets. |
| `notify.group` | string | — | Name of an [alert group](#notifications-webhooks-email--alert-groups) — a named fan-out of 1–16 channels managed in the console or via `POST /api/alert-groups/{name}`. When set it **wins over** `notify.url` and every channel in the group is alerted. |

Cron schedules are evaluated in **UTC** by default; prefix the expression with `CRON_TZ=<zone>` to evaluate it in a specific timezone:

```yaml
schedule: "CRON_TZ=Asia/Shanghai 0 2 * * *"   # 02:00 Shanghai time, every day
```

> `paused` is **not** a YAML field. Pausing is operational state managed from the console, CLI (`cronova pause <dag_id>`), or API, and is preserved across DAG reloads.

### Notifications: webhooks, email & alert groups

`notify.url` accepts two kinds of targets:

- an `http(s)://` **incoming-webhook** URL — the run summary is POSTed as JSON, shaped by `notify.format`;
- `mailto:addr[,addr]` — the alert is sent as **email** through the server's SMTP relay. This requires the `smtp:` section of the server config to be filled in; without it, mail channels fail delivery (logged, never blocking the scheduler).

Instead of pasting the same URL into every DAG, `notify.group` references a named **alert group**: a reusable bundle of 1–16 channels, each with its own URL (webhook or `mailto:`) and format. Groups are managed in the console (Variables & Connections → Alert groups) or via the API (`GET /api/alert-groups`, `POST`/`DELETE /api/alert-groups/{name}`), and one run alert fans out to every channel of the group.

```yaml
notify:
  group: oncall      # alert every channel of the "oncall" group
  on: [failure]
```

Resolution is most-specific-first: a set `notify.group` wins over `notify.url`; a group name that no longer resolves (e.g. the group was deleted) falls back to the DAG's own `notify.url`, and then to the instance-wide default notify target — a dangling reference is logged loudly but never loses the alert.

## Definition snapshots

Every run stores the exact canonical YAML and SHA-256 definition hash it started
with. Editing a DAG while a run is active therefore changes future runs only;
the active run keeps its original task graph and cannot be wedged by a removed
or renamed task. An explicit retry is the exception: it intentionally adopts
the latest DAG definition, records that definition hash on the new attempts,
and leaves removed task instances as historical rows rather than dispatching
them again.

## Task-level fields

Each entry under `tasks:` describes one task.

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | — (required) | Task identifier, unique within the DAG. |
| `type` | string | `shell` | One of `shell`, `python`, `sql`, `jar`, `http`, `subdag`. See [Task types](#task-types). |
| `command` | string | — | The command (shell), code (python), or query (sql). Supports [template variables](#template-variables). Not used for `http`. |
| `deps` | list of task ids | — | Upstream tasks that must satisfy this task's `trigger_rule` before it runs. Edges are cycle-checked. |
| `pool` | string | `default` | The [resource pool](#resource-pools) this task consumes a slot from. |
| `priority` | int | `0` | Higher runs first when tasks contend for the same pool. |
| `worker_group` | string | inherits DAG `worker_group` | Routes this task to a group of dial-in remote workers (the workers' `group` label, default `"default"`). Empty (and no DAG-level default) = run on the scheduler's local executor. |
| `retries` | int | inherits `default_retries` | Times to retry on failure. |
| `retry_delay` | int (seconds) | inherits `default_retry_delay` | Delay between retries. |
| `retry_backoff` | string | `fixed` | How the wait between retries grows: `fixed` (constant `retry_delay`) or `exponential` (waits `retry_delay·2^(n-1)` before the n-th retry). |
| `retry_delay_max` | int (seconds) | `0` | Caps the exponential wait. `0` = no explicit cap (a built-in 24h safety ceiling still applies). |
| `timeout` | int (seconds) | `0` | Per-attempt execution timeout; on breach the whole process group is killed. `0` = none. |
| `sla` | int (seconds) | `0` | Task soft deadline from run start; breach alerts only. |
| `trigger_rule` | string | `all_success` | When to run relative to upstream states. See [Trigger rules](#trigger-rules). |
| `when` | string | — | Runtime condition template (e.g. `"{{ params.env }}"` or `"{{ ti.check.proceed }}"`), evaluated once the task is otherwise ready. A falsy render (`""`, `false`, `0`, `no`, or an unresolved placeholder) marks the task **skipped**. |
| `foreach` | list of strings | — | Fans the task out into one task per item at definition time: ids become `<id>_<index>`, `{{ item }}` / `{{ item_index }}` are substituted in `command`/`when`, and downstream `deps` on the original id cover every shard. Each shard keeps its own retries, log, and state. |
| `conn` | string | — | Connection id for a `sql` task (selects driver + builds the DSN). |
| `project` | string | — | Name of an uploaded project directory to stage as the working directory (shell tasks; not combinable with `worker_group`). See [Getting Started → Projects](GETTING_STARTED.md). |
| `http` | object | — | HTTP request spec for `http` tasks (see below). |
| `subdag` | string | — | For `type: subdag`: the DAG to run as a **sub-workflow**. The task launches a linked child run (visible in run history with trigger type `subdag` and a parent link) and mirrors its terminal state. Cancelling the parent cascades to the child; a task retry starts a fresh child run (the old one stays as history). Nesting is capped at 5 levels as a cycle backstop. |
| `depends_on_dag` | object | — | Cross-DAG **wait**: hold this task until another DAG's matching period run has *succeeded*. Fields: `dag` (target id), `offset` (which period, in [date-expression](#date-expressions) offset grammar — `""`/`same`, `- 1d`, `.month_start`…), `timeout` (seconds from run start; 0 = wait until `dagrun_timeout`), `on_timeout` (`fail` default, or `skip`). A failed target run keeps the wait alive (it may be retried); only the timeout resolves the standoff. |

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
| `subdag` | scheduler-internal (child run) | — (use the `subdag:` field) | nothing extra |

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

When the DAG declares a `timezone:`, `logical_date`/`logical_datetime` render in
that zone (the run's own calendar day), while storage stays UTC.

### Date expressions

`logical_date` / `logical_datetime` accept offsets, anchors, and custom
formats directly inside the placeholder:

```
{{ logical_date[.anchor][ ±N<unit> ]... [| format] }}
```

| Piece | Values | Notes |
|---|---|---|
| anchor | `.month_start` `.month_end` `.week_start` `.week_end` | Binds to the base name, applies first; weeks start Monday; time resets to midnight. |
| offset | `±N` + `d` (days) `h` (hours) `w` (weeks) `mo` (months) | Repeatable, applied left to right. `d`/`w`/`mo` are calendar arithmetic (wall clock survives DST); `h` is an absolute duration. |
| format | `\|` + strftime subset: `%Y %y %m %d %H %M %S %%` | Default: `YYYY-MM-DD` for `logical_date`, RFC3339 for `logical_datetime`. |

Examples:

```yaml
command: "python etl.py --day {{ logical_date - 1d | %Y%m%d }}"     # yesterday as 20260807
command: "report.sh --from {{ logical_date.month_start }} --to {{ logical_date.month_end }}"
command: "cleanup.sh --before {{ logical_date.month_start - 1d }}"  # last day of previous month
command: "sync.sh --since {{ logical_datetime - 6h }}"
```

An expression that does not parse (unknown unit, bad `%` token, stray text) is
left in the command verbatim — typos stay visible in the task log instead of
silently rendering empty.

Shell tasks do not inherit the scheduler's complete process environment. Cronova
passes a small runtime-safe set (`PATH`, locale, home/temp and certificate
variables) plus the task-specific `CRONOVA_*` values above. This prevents server
credentials such as `CRONOVA_ADMIN_PASSWORD` from reaching task code. Add a
parent variable explicitly with `CRONOVA_TASK_ENV_ALLOWLIST=name1,name2`, or put
the value in the task's resolved environment instead.

Plus UI-managed references, resolved server-side (secrets never enter the blanket env):

- `{{ var.KEY }}` — a shared [variable](AGENTS.md).
- `{{ conn.ID.FIELD }}` — a connection field: `host`, `port`, `login` (alias `user`), `password`, `type`, or an extra JSON field as `extra.KEY`.
- `{{ params.KEY }}` — a manual-trigger parameter (also injected as `CRONOVA_PARAM_<KEY>`). Event-triggered runs receive the event payload as params plus `{{ params.event_key }}`.
- `{{ ti.TASK_ID.KEY }}` — a field of an upstream task's emitted output (this run only).

### Passing data between tasks

A task can hand small values (row counts, generated file paths, ids) to its
downstream tasks by writing a **flat JSON string map** to the file named in
`$CRONOVA_OUTPUT` (up to 64 KB):

```yaml
tasks:
  - id: produce
    command: 'echo "{\"rows\":\"1234\"}" > "$CRONOVA_OUTPUT"'
  - id: consume
    command: 'echo upstream wrote {{ ti.produce.rows }} rows'
    deps: [produce]
```

The output is collected when the task finishes successfully and stored per
(run, task); trigger rules guarantee the upstream finished before a downstream
referencing it is dispatched. This is metadata passing, not a data channel —
move real datasets through external storage.

### Self-skipping tasks

A task that exits with code **99** is recorded as `skipped` instead of failed —
the shell-level way to say "nothing to do here today". Combine with downstream
`trigger_rule: none_failed` (skip passes through) or the default `all_success`
(skip blocks) to shape what happens next; `when:` (see task fields) is the
declarative alternative evaluated before the task even starts.

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

Every task defaults to the `default` pool. See the [CLI Reference](CLI.md) and [Architecture](ARCHITECTURE.md).

## See also

- [Getting Started](GETTING_STARTED.md) · [CLI Reference](CLI.md) · [AI Agents (MCP)](AGENTS.md) · [Deployment](DEPLOY.md) · [Architecture](ARCHITECTURE.md) · [FAQ](FAQ.md)
