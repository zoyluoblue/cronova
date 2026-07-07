# Retries, timeouts & pools

Real pipelines fail: an API drops a connection, a query hangs, ten heavy jobs land on the same box at once. This chapter makes your workflow resilient with automatic **retries**, per-attempt **timeouts**, soft **SLAs**, a hard run deadline, and global concurrency **pools** — the features that separate a workflow scheduler from a plain cron job.

## A flaky task

Let's build a task that fails on purpose. Every attempt gets an attempt number — `{{ try_number }}` in templates, `CRONOVA_TRY_NUMBER` in the environment — starting at `1` and incrementing on each retry. We'll use it to simulate a fetch that only succeeds on the third try.

Create `dags/flaky_pipeline.yaml`:

```yaml
dag_id: flaky_pipeline
tasks:
  - id: fetch
    type: shell
    command: |
      if [ "$CRONOVA_TRY_NUMBER" -lt 3 ]; then
        echo "attempt $CRONOVA_TRY_NUMBER: connection reset by peer" >&2
        exit 1
      fi
      echo "attempt $CRONOVA_TRY_NUMBER: fetched 1200 rows"
    retries: 3
    retry_delay: 5
```

Two new task fields:

- `retries: 3` — retry up to 3 times on failure. The task gets **`retries` + 1 total attempts** (here: 4).
- `retry_delay: 5` — wait 5 seconds between attempts. The default for both is `0`.

There's no `schedule`, so the DAG only runs when you trigger it. Do that now:

```bash
cronova trigger flaky_pipeline
```

Then watch the run:

```bash
cronova runs flaky_pipeline
```

For the first two attempts you'll see the task cycle through `running` and `up_for_retry` — the state a failed attempt lands in while it waits out its `retry_delay`:

```
RUN_ID                                  LOGICAL_DATE          STATE    TRIGGER  TASKS
flaky_pipeline__manual_1751871234...    2026-07-07T00:00:00Z  running  manual   fetch=up_for_retry
```

About ten seconds later (two failures × 5s delay), run it again — the third attempt succeeds and the run finishes:

```
RUN_ID                                  LOGICAL_DATE          STATE    TRIGGER  TASKS
flaky_pipeline__manual_1751871234...    2026-07-07T00:00:00Z  success  manual   fetch=success
```

Open the run in the console at [http://localhost:8090](http://localhost:8090) and click the `fetch` task: the log shows each attempt — `attempt 1: connection reset by peer`, `attempt 2: …`, and finally `attempt 3: fetched 1200 rows`. The task's try number is stored per attempt, so you can always tell how hard a task had to work.

!!! tip
    Automatic retries handle transient failures. For a run that already **finished** as failed, use the operator command `cronova retry <run_id> [task_id]` to re-run just the failed tasks — see the [CLI Reference](../CLI.md#agent--remote-mode).

## DAG-level defaults

Setting `retries` on every task gets repetitive. Set a default once at the DAG level:

```yaml
dag_id: flaky_pipeline
default_retries: 2
default_retry_delay: 30
tasks:
  - id: fetch
    ...            # inherits: 2 retries, 30s apart
  - id: load
    retries: 5     # tasks can still override the default
    ...
```

`default_retries` and `default_retry_delay` apply to every task that doesn't set its own `retries` / `retry_delay`. Both default to `0`.

## Timeouts: kill a stuck attempt

A retry only helps if the attempt actually *fails*. A hung process — a stuck connection, a lock that never releases — would otherwise run forever. `timeout` puts a per-attempt limit on it. Add a second task:

```yaml
  - id: transform
    type: shell
    command: "sleep 120"
    deps: [fetch]
    timeout: 5
```

`timeout: 5` gives each attempt 5 seconds. On breach, cronova kills **the whole process group** — not just the top-level shell, but every child process it spawned — so nothing keeps running in the background. The default is `0` (no limit).

Trigger the DAG again and check the `transform` log in the console after a few seconds. The `sleep` never finishes; instead the log ends with:

```
=== killed: timeout after 5s ===
```

The killed attempt exits with code `124` and counts as a normal failure — so if the task has `retries` left, it goes to `up_for_retry` and gets another attempt with a fresh clock. With no retries remaining, it finalizes as `failed` and its downstream tasks become `upstream_failed`.

## SLA: a soft deadline that alerts

Sometimes you don't want to kill anything — you just want to *know* when things run late. That's `sla`, available at both levels and always measured **from run start**:

```yaml
dag_id: flaky_pipeline
sla: 600                 # alert if the whole run exceeds 10 minutes
notify:
  - url: https://hooks.example.com/cronova
    on: [failure]
tasks:
  - id: transform
    sla: 300             # alert if this task hasn't finished 5 minutes into the run
    ...
```

When a run (or a still-unfinished task) crosses its `sla`, cronova logs a warning and fires the DAG's `notify:` webhook with an `sla_miss` (or `task_sla_miss`) payload. The run **keeps going** — an SLA is purely an alert, raised at most once per run or task. Setting the threshold is itself the opt-in: SLA alerts fire on any configured webhook regardless of the `on:` list (which only gates the success/failure alerts at the end of a run).

!!! note
    Task `sla` is a deadline from **run start**, not from when the task starts. If upstream tasks eat the budget, a downstream task can miss its SLA before executing a single line — which is exactly what you want to hear about.

## dagrun_timeout: the hard stop

`sla` warns; `dagrun_timeout` acts. It's the run-level hard deadline, also in seconds from run start:

```yaml
dag_id: flaky_pipeline
sla: 600
dagrun_timeout: 1800     # kill the whole run after 30 minutes
```

On breach, cronova kills every running task, marks all unfinished tasks `timed_out`, finalizes the run as `timed_out`, and — if a `notify:` webhook is configured — sends a failure alert (again, not gated by `on:`). The default `0` means no limit.

A good pattern is to pair them: `sla` at the duration you *expect*, `dagrun_timeout` at the duration you can't *tolerate*.

## Resource pools: global concurrency limits

Retries and timeouts protect a single task. **Pools** protect shared resources — a database that can take 4 concurrent report queries, an API with a strict rate limit — across *all* DAGs. A pool is a named set of global slots; a task occupies one slot of its pool while running.

Create a pool from the CLI:

```bash
cronova pools set reports 4
```

```
pool "reports" set to 4 slots
```

Then point tasks at it with `pool:`, and rank them with `priority:`:

```yaml
  - id: build_report
    type: shell
    command: "python report.py --date {{ logical_date }}"
    deps: [transform]
    pool: reports
    priority: 10
```

No matter how many DAG runs are active, at most 4 tasks in the `reports` pool execute at once. When more tasks are waiting than there are free slots, higher `priority` wins (the default is `0`).

Every task that doesn't set `pool:` uses the built-in `default` pool, created with 16 slots. Check what exists and resize any pool at any time:

```bash
cronova pools
```

```
NAME     SLOTS
default  16
reports  4
```

!!! warning
    If a task references a pool that doesn't exist yet, cronova auto-creates it with the default 16 slots so nothing deadlocks — probably not the limit you intended. Create the pool with `cronova pools set` *before* shipping the DAG.

## What you learned

- `retries` / `retry_delay` (and DAG-level `default_retries` / `default_retry_delay`) give a task `retries + 1` attempts, with `try_number` counting up and `up_for_retry` bridging the delay.
- `timeout` kills a stuck attempt's whole process group; `sla` (task or DAG) is a soft, alert-only deadline from run start; `dagrun_timeout` hard-stops the entire run.
- Pools cap concurrency globally: `cronova pools set reports 4`, then `pool:` + `priority:` on tasks; everything else shares the 16-slot `default` pool.

Next: chain whole workflows together with `trigger_after` and webhook notifications in [Cross-DAG dependencies](cross-dag.md).
