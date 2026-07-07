# Dependencies & trigger rules

In this chapter you'll wire tasks into a real dependency graph with `deps`, see what happens downstream when a task fails, and take control of that behavior with **trigger rules** — plus the two operator tools for recovering a bad run: `cronova retry` and `cronova mark`.

## Wire tasks together with `deps`

A cronova **DAG** is a directed acyclic graph: tasks are the nodes, and each task's `deps` list draws the incoming edges. A task runs only when its upstream tasks (the ones it names in `deps`) have reached the states its trigger rule demands — by default, when they have all **succeeded**.

Create `dags/daily_etl.yaml`, a classic extract → transform → load chain. Leave `schedule` out so it only runs when you trigger it:

```yaml
dag_id: daily_etl
tasks:
  - id: extract
    type: shell
    command: echo "extracting rows"
  - id: transform
    type: shell
    command: echo "transforming"
    deps: [extract]
  - id: load
    type: shell
    command: echo "loading"
    deps: [transform]
```

`deps` is a list, so graphs don't have to be straight lines: a task with `deps: [a, b]` fans in and waits for both, and two tasks that both name `deps: [extract]` fan out and run in parallel.

Every DAG file is validated and **cycle-checked** on load. If you accidentally wire a loop (say, `extract` depending on `load`), the file is rejected with a `dependency cycle detected` error in the `serve` log and skipped — a cycle can never silently deadlock the workflow scheduler.

**Check it** — trigger the DAG and watch the chain execute in order:

```bash
cronova trigger daily_etl
cronova runs daily_etl
```

```
RUN_ID                                 LOGICAL_DATE          STATE    TRIGGER  TASKS
daily_etl__manual_1783472901234567000  2026-07-07T09:08:21Z  success  manual   extract=success transform=success load=success
```

In the console at **http://localhost:8090**, open `daily_etl` and you'll see the same graph drawn as nodes and edges — the visual counterpart of your `deps` lists.

## When an upstream fails: `upstream_failed`

What happens to `load` if `transform` fails? Try it. Change the `transform` command to fail on purpose:

```yaml
  - id: transform
    type: shell
    command: exit 1
    deps: [extract]
```

Save, trigger again, and check:

```bash
cronova trigger daily_etl
cronova runs daily_etl -n 1
```

```
RUN_ID                                 LOGICAL_DATE          STATE   TRIGGER  TASKS
daily_etl__manual_1783473010987654000  2026-07-07T09:10:10Z  failed  manual   extract=success transform=failed load=upstream_failed
```

`load` never ran. When an upstream task fails, cronova marks the tasks below it **`upstream_failed`** — a terminal state meaning "blocked by an upstream failure, never executed". Propagation follows the edges: only the failed task's descendants are blocked, while unrelated parallel branches of the same run keep executing to completion. A task can even be caught by this while already queued, if its upstream fails before the executor picks it up.

Any `failed` or `upstream_failed` task makes the whole run finish as `failed` — which is what you see in the `STATE` column.

## Trigger rules

The default gate — "run when **all** upstreams succeeded" — is one of six. Set a task's `trigger_rule` to change when it fires relative to its `deps`:

| Rule | Runs when |
|---|---|
| `all_success` (default) | every upstream task succeeded |
| `all_done` | every upstream task finished (any state) |
| `all_failed` | every upstream task failed |
| `one_success` | at least one upstream task succeeded |
| `one_failed` | at least one upstream task failed |
| `none_failed` | no upstream task failed (success or skipped) |

Two of these solve everyday problems. A **cleanup** task should run whether the pipeline worked or not — that's `all_done`. An **alert** task should run precisely *because* something failed — that's `one_failed`. Add both to `daily_etl` (keep `transform` failing for now):

```yaml
  - id: cleanup
    type: shell
    command: echo "removing temp files"
    deps: [extract, transform, load]
    trigger_rule: all_done
  - id: alert
    type: shell
    command: echo "ALERT daily_etl failed"   # curl your pager here
    deps: [transform, load]
    trigger_rule: one_failed
```

**Check it** — trigger once more:

```bash
cronova trigger daily_etl
cronova runs daily_etl -n 1
```

```
RUN_ID                                 LOGICAL_DATE          STATE   TRIGGER  TASKS
daily_etl__manual_1783473120123456000  2026-07-07T09:12:00Z  failed  manual   extract=success transform=failed load=upstream_failed cleanup=success alert=success
```

`transform` failed and `load` was blocked exactly as before — but `cleanup` ran anyway (`all_done`), and `alert` fired because a dependency failed (`one_failed`). Open the `alert` task's log in the console to see the message.

!!! warning

    A task whose trigger rule can **never** be satisfied anymore is marked
    `upstream_failed` — and any `upstream_failed` task makes the run's recorded
    state `failed`. On a fully green run, an unconditional `one_failed` alert
    task can never fire, so it ends `upstream_failed` and the *successful*
    pipeline is recorded as a failed run. Use `one_failed` / `all_failed`
    branches to *react* to failures inside a run you already expect to be red;
    for plain "notify me when a run fails", prefer the DAG-level `notify`
    webhook (see the [DAG Reference](../DAG_REFERENCE.md)) and remove the
    alert task before putting this DAG on a schedule.

## Retry a failed run: `cronova retry`

Now fix the bug — restore `transform` to a working command:

```yaml
  - id: transform
    type: shell
    command: echo "transforming"
    deps: [extract]
```

Saving the file doesn't rewrite history: the failed run stays failed. To re-run just the broken parts, use `cronova retry` with the run id from `cronova runs`:

```bash
cronova retry daily_etl__manual_1783473120123456000 -server http://localhost:8090
```

```
{
  "retried": true
}
```

!!! note

    `retry`, `mark`, and `cancel` are operator verbs that talk to the running
    server's REST API, so they need a target: pass
    `-server http://localhost:8090` (or export `CRONOVA_SERVER`). If you
    enabled login with `-auth`, also supply an API token via `-token` /
    `CRONOVA_TOKEN` — mint one with `cronova tokens create`. All the details
    are in the [CLI Reference](../CLI.md).

A retry re-queues every `failed`, `upstream_failed`, and `cancelled` task — plus everything downstream of them — and reactivates the run. Tasks that already succeeded keep their results and are not re-run. The re-queued tasks execute against the **current** DAG definition, so the fix-then-retry loop is exactly: edit the YAML, save, retry. If the run is still active, or nothing in it failed, the API answers with a conflict instead of retrying.

You can also target a single task; its downstream tasks are cleared with it:

```bash
cronova retry daily_etl__manual_1783473120123456000 transform -server http://localhost:8090
```

**Check it:**

```bash
cronova runs daily_etl -n 1
```

The run flips back to `running`, `transform` and `load` execute again, and the `STATE` column lands on `success`. In the console, the same run's history now shows the fresh attempts.

!!! tip

    Manual `retry` is for after-the-fact recovery. For failures you can
    anticipate — flaky networks, busy databases — give tasks automatic
    `retries` and `retry_delay` instead, covered in
    [Retries, timeouts & pools](retries-timeouts-pools.md).

## Override a state by hand: `cronova mark`

Sometimes re-running is wrong — you already fixed the data by hand, or a task is stuck and you want the pipeline to move on. `cronova mark` is the operator override:

```bash
cronova mark <run_id> <state>              # run:  success | failed
cronova mark <run_id> <task_id> <state>    # task: success | failed | skipped
```

Say `transform` failed but you ran the transformation manually. Mark it done and let the run continue:

```bash
cronova mark daily_etl__manual_1783473120123456000 transform success -server http://localhost:8090
```

```
{
  "marked": true
}
```

Marking a task `success` or `skipped` releases the downstream tasks that were `upstream_failed` because of it — the scheduler picks them up on the next tick and the run resumes from where it was blocked. Marking works on an active run too: a still-running task's process is killed first, then the state you chose wins.

One subtlety: the default `all_success` rule treats a **skipped** upstream as blocking. If a task should tolerate skipped upstreams, give it `trigger_rule: none_failed` — "no upstream failed; success or skipped is fine".

Run-level `mark` corrects a *finished* run's recorded outcome — for example, declaring a run `success` after you've dealt with its failure out-of-band:

```bash
cronova mark daily_etl__manual_1783473120123456000 success -server http://localhost:8090
```

**Check it** — `cronova runs daily_etl -n 1` reflects the override immediately, and every `trigger`, `cancel`, `retry`, and `mark` is recorded in the server's operations audit log, so overrides are never invisible.

## What you learned

- `deps` draws the graph edges; every DAG file is cycle-checked on load, and a task fires when its upstreams satisfy its `trigger_rule` (default: `all_success`).
- A failure blocks only its descendants — they end `upstream_failed` — while parallel branches finish; any failed or blocked task makes the run `failed`.
- Six trigger rules cover the rest: `all_done` for cleanup, `one_failed` for in-run failure handling, `none_failed` to tolerate skipped upstreams, plus `one_success` and `all_failed`.
- `cronova retry <run_id> [task_id]` re-queues the failed parts of a finished run against the current DAG definition; `cronova mark <run_id> [task_id] <state>` is the manual override that can unblock or correct a run.

**Next:** parameterize your commands with the run's logical date and friends in [Template variables](template-variables.md).
