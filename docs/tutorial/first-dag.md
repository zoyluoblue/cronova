# Your first DAG

In this chapter you'll write your first **DAG** — a workflow of two shell tasks connected by a dependency — then run it with the cronova scheduler and watch it succeed, from both the CLI and the web console.

## Create the DAG file

A DAG (directed acyclic graph) is a set of tasks with dependency edges, defined as a single YAML file in the `./dags/` directory. Each shell task runs as an OS subprocess.

Create `dags/hello.yaml`:

```yaml
dag_id: hello
tasks:
  - id: greet
    type: shell
    command: echo "hello from cronova"
  - id: report
    type: shell
    command: echo "run $CRONOVA_RUN_ID finished greeting"
    deps: [greet]
```

That's a complete, runnable workflow. Let's walk through it line by line.

### `dag_id`

```yaml
dag_id: hello
```

The unique identifier for the DAG. It's required, and it's the name you'll use everywhere else — `cronova trigger hello`, the console's DAG list, the run history.

### `tasks`

```yaml
tasks:
  - id: greet
```

The list of tasks — also required. Each entry gets an `id` that must be unique within the DAG.

### `type`

```yaml
    type: shell
```

The task type. `shell` runs `command` as an OS subprocess via `sh -c`, so anything you can type in a terminal works here. There are four more types — `python`, `sql`, `jar`, and `http` — covered later in the tutorial.

!!! tip

    `shell` is the default `type`, so you could omit these two lines entirely.
    We spell it out here so the file reads unambiguously.

### `command`

```yaml
    command: echo "run $CRONOVA_RUN_ID finished greeting"
```

What the task executes. Notice `$CRONOVA_RUN_ID`: cronova injects run information into every task's environment as `CRONOVA_*` variables — the run id, the DAG id, the task id, the logical date, and more. Your scripts can use them without any wiring.

### `deps`

```yaml
    deps: [greet]
```

The dependency edge — this is what makes it a graph. `report` waits for `greet` and (by default) runs only after `greet` **succeeds**. Edges are cycle-checked when the file loads, so an accidental loop is rejected instead of hanging your workflow.

## Start the scheduler

`cronova serve` runs the scheduling loop, the REST API, and the web console in one process:

```bash
cronova serve
```

It loads every `*.yaml` and `*.yml` file from `./dags`. A malformed file is logged and skipped — it never takes the scheduler down.

**Check it** — from a second terminal:

```bash
cronova dags
```

```
DAG_ID  SCHEDULE  CATCHUP  PAUSED  MAX_ACTIVE
hello   (manual)  false    false   1
```

`hello` is registered. You can also open the console at **http://localhost:8090** — the DAG list shows `hello` with the same details.

## Trigger a run

```bash
cronova trigger hello
```

```
created run hello__manual_1783468804512345600 (a running `cronova serve` will execute it)
```

!!! note

    `cronova trigger` only **creates** the run — it writes a run row to the
    database and returns immediately. The running `cronova serve` picks it up
    on its next scheduling tick (default every `2s`), so execution starts
    almost instantly, just not inside the `trigger` command itself.

## Watch it run

```bash
cronova runs hello
```

```
RUN_ID                              LOGICAL_DATE          STATE    TRIGGER  TASKS
hello__manual_1783468804512345600   2026-07-07T08:15:04Z  success  manual   greet=success report=success
```

The run went `greet` first, then `report` — exactly the order `deps` dictates. If you run `cronova runs hello` quickly enough after triggering, you'll catch the states advancing: `queued` → `running` → `success`.

### The same thing in the console

Open **http://localhost:8090** and you'll find the console equivalents of everything you just did:

- The **DAG list** shows `hello`, with a one-click manual trigger — no terminal needed.
- Click into `hello` for the **run history** and per-task states of each run.
- Click a task instance for **live log tailing** — you'll see `hello from cronova` in the `greet` log, and the actual run id printed by `report`.

## Scheduled vs. manual

You may have noticed `hello.yaml` has no `schedule` field. That's deliberate: **omitting `schedule` makes the DAG manual-only** — it runs when you trigger it (from the CLI, the console, or the API) and never by itself. That's why `cronova dags` shows `(manual)` in the SCHEDULE column.

To make cronova run it on its own, add one line — a cron expression or an interval:

```yaml
dag_id: hello
schedule: "@every 1m"    # or a cron expression like "0 2 * * *"
tasks:
  # ...unchanged...
```

Save the file, and `cronova dags` now shows `SCHEDULE=@every 1m` — the scheduler creates a new run every minute. Schedules, `start_date`, and catchup/backfill get their own chapter next.

!!! warning

    A schedule of `@every 1m` keeps producing runs for as long as `serve` is
    up. For experiments, either remove the `schedule` line again or pause the
    DAG from the console (or `cronova pause hello`) when you're done.

## What you learned

- A DAG is one YAML file in `./dags`: a required `dag_id` plus a `tasks` list, with `deps` drawing the dependency edges.
- `cronova serve` runs the scheduler and the console at http://localhost:8090; `cronova dags` lists what it loaded.
- `cronova trigger <dag_id>` creates a run and `serve` executes it on the next tick; `cronova runs <dag_id>` and the console's run history show every task's state.
- No `schedule` field means manual-only; adding one hands the DAG to the scheduler.

The full field-by-field schema is in the [DAG & Task Reference](../DAG_REFERENCE.md). **Next:** put `hello` on a real schedule — cron expressions, intervals, `start_date`, and catchup — in [Schedules](scheduling.md).
