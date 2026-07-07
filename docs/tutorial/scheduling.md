# Scheduling & catchup

So far you have triggered your DAG by hand. In this chapter you give it a **schedule**, learn the *logical date* — the mental model behind backfills — and control how runs pile up with `catchup`, `max_active_runs`, and pausing.

## The `schedule` field

A DAG's `schedule` accepts two forms: a **cron expression** or an **interval**.

### Cron expressions

Standard 5-field cron syntax, exactly what you'd write in a `crontab`. Create `dags/daily_report.yaml`:

```yaml
dag_id: daily_report
schedule: "0 2 * * *"        # every day at 02:00
start_date: 2026-07-01
tasks:
  - id: build
    type: shell
    command: echo "reporting for {{ logical_date }}"
```

The scheduler evaluates every DAG's schedule on each tick (default every `2s`, tunable with `serve -tick`) and creates a run whenever a schedule boundary has passed.

Check it — list your DAGs:

```bash
./cronova dags
```

```text
DAG_ID        SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
daily_report  0 2 * * *  false    false   1
```

The same schedule appears next to the DAG in the console at [http://localhost:8090](http://localhost:8090).

!!! note

    The scheduler works in **UTC**: cron boundaries fire on UTC time, and every
    run's logical date is recorded in UTC. `"0 2 * * *"` means 02:00 UTC, not
    02:00 local time.

### Intervals with `@every`

For "just run this every N seconds/minutes/hours", skip cron math and use an interval:

```yaml
dag_id: ticker
schedule: "@every 30s"
start_date: 2026-06-01
tasks:
  - id: heartbeat
    type: shell
    command: echo "tick at $CRONOVA_LOGICAL_DATETIME"
```

Wait a minute, then check it:

```bash
./cronova runs ticker -n 3
```

```text
RUN_ID                     LOGICAL_DATE          STATE    TRIGGER   TASKS
ticker__20260707T091530Z   2026-07-07T09:15:30Z  success  schedule  heartbeat=success
ticker__20260707T091500Z   2026-07-07T09:15:00Z  success  schedule  heartbeat=success
ticker__20260707T091430Z   2026-07-07T09:14:30Z  success  schedule  heartbeat=success
```

New runs arrive every 30 seconds, each with `TRIGGER` = `schedule`. A runnable version of this DAG ships in the repo: [`dags/ticker.yaml`](https://github.com/zoyluoblue/cronova/blob/main/dags/ticker.yaml).

### No schedule = manual only

Omit `schedule` (or set it to `""`) and the DAG never runs on a clock — it shows as `(manual)` in `cronova dags` and runs only when you trigger it from the console, run `cronova trigger <dag_id>`, or wire it to another DAG with `trigger_after` (covered in a later chapter).

## `start_date`: where time begins

`start_date` is the earliest logical date the DAG can be scheduled for. On its own it does little — but it is the anchor that catchup counts backward to, so give every scheduled DAG one.

```yaml
start_date: 2026-07-01
```

## The logical date

Here is the core mental model of any DAG-based workflow scheduler, cronova included:

**Every run carries a `logical_date` — the period the run *represents*, not the wall-clock moment it happens to execute.**

The 02:00 run for July 6th has `logical_date = 2026-07-06`, even if the scheduler was down that night and only executes the run on July 7th. Your task reads the logical date instead of asking the system clock:

```yaml
tasks:
  - id: build
    type: shell
    command: python report.py --date {{ logical_date }}      # via template
  - id: notify
    type: shell
    command: echo "built report for $CRONOVA_LOGICAL_DATE"   # via env var
    deps: [build]
```

`{{ logical_date }}` renders as `YYYY-MM-DD`; `{{ logical_datetime }}` gives the full RFC 3339 timestamp. Both are also injected into the task's environment as `CRONOVA_LOGICAL_DATE` and `CRONOVA_LOGICAL_DATETIME`. Even the run id embeds it: `daily_report__20260706T020000Z`.

A task that only knows "now" makes backfilling meaningless — every replayed run would process *today's* data. A task keyed on `{{ logical_date }}` processes the right period no matter when it runs. That is what makes the next section work.

## `catchup`: backfilling missed periods

`catchup` decides what happens to schedule boundaries that passed while the DAG didn't run — because the scheduler was down, the DAG was just created, or its `start_date` is in the past:

- `catchup: false` (the default) — missed periods are skipped; only future boundaries create runs.
- `catchup: true` — the scheduler walks every boundary from `start_date` to now and creates one run per missed period, each with its own logical date.

Try it. Today is 2026-07-07; date the DAG a week back and enable catchup:

```yaml
dag_id: daily_report
schedule: "0 2 * * *"
start_date: 2026-07-01
catchup: true
tasks:
  - id: build
    type: shell
    command: echo "reporting for {{ logical_date }}"
```

Check it — within a few seconds of saving the file:

```bash
./cronova runs daily_report -n 10
```

```text
RUN_ID                           LOGICAL_DATE          STATE    TRIGGER   TASKS
daily_report__20260707T020000Z   2026-07-07T02:00:00Z  running  schedule  build=running
daily_report__20260706T020000Z   2026-07-06T02:00:00Z  success  schedule  build=success
daily_report__20260705T020000Z   2026-07-05T02:00:00Z  success  schedule  build=success
daily_report__20260704T020000Z   2026-07-04T02:00:00Z  success  schedule  build=success
...
```

One run per missed day, oldest first, each processing "its" date. Open the DAG in the console and watch the run history fill in; the log for each run echoes a different `{{ logical_date }}`.

Backfills are deliberately throttled: the scheduler creates at most one new run per tick and never exceeds `max_active_runs`, so enabling catchup on a year-old `start_date` produces a steady drain, not a flood.

!!! warning

    Catchup (and retries) assume your tasks are **idempotent**: running the same
    logical date twice must produce the same result. If a task appends rather
    than overwrites, a backfill will duplicate data.

## `max_active_runs`

`max_active_runs` caps how many runs of *this DAG* may be in flight at once. The default is `1` (a `0` is treated as `1`), which means a backfill executes strictly one period at a time, in order — usually what you want when day N+1 depends on day N's output.

If your periods are independent and you want a faster backfill, raise it:

```yaml
max_active_runs: 3
```

Check it: during a backfill, `./cronova runs daily_report` now shows up to three runs in `running` state simultaneously.

## Pausing a DAG

Pausing stops the scheduler from creating new runs without touching the YAML. Flip the pause toggle on the DAG in the console, or use the CLI — `pause` talks to the running server's REST API, so point the CLI at it first:

```bash
export CRONOVA_SERVER=http://localhost:8090
./cronova pause daily_report
```

Check it:

```bash
./cronova dags
```

```text
DAG_ID        SCHEDULE   CATCHUP  PAUSED  MAX_ACTIVE
daily_report  0 2 * * *  true     true    1
```

`PAUSED` is now `true`, and no new scheduled runs appear. Resume with:

```bash
./cronova pause daily_report -off
```

If you enabled authentication, also set `CRONOVA_TOKEN` (mint one with `cronova tokens create` — see the [CLI Reference](../CLI.md)).

!!! tip

    `paused` is *not* a YAML field — it is operational state, managed from the
    console, CLI, or API, and it survives DAG file reloads. Your DAG definition
    stays a pure description of the workflow; pausing stays an ops action.

## What you learned

- `schedule` takes a cron expression (`"0 2 * * *"`) or an interval (`"@every 30s"`); omit it for manual-only DAGs. Times are UTC.
- Every run has a **logical date** — the period it represents — available as `{{ logical_date }}` / `CRONOVA_LOGICAL_DATE`.
- `catchup: true` backfills one run per missed period from `start_date`, throttled by `max_active_runs` (default `1`).
- Pause and resume scheduling from the console or with `cronova pause <dag_id> [-off]` — it's operational state, not YAML.

Next: fan your workflow out into multiple tasks and control exactly when each one fires in [Dependencies & trigger rules](dependencies.md).
