# Template variables

So far every command in your pipeline is static text. This chapter shows how to inject per-run values — the logical date, the run id, the attempt number — into any task, either as `{{ ... }}` templates in the command or as `CRONOVA_*` environment variables in your script.

## The six built-in variables

Every task attempt gets six built-in run variables. Each one is available **two ways**: as a `{{ name }}` placeholder substituted into the `command` at dispatch, and as an environment variable injected into the task's process:

| Template | Env var | Value |
|---|---|---|
| `{{ logical_date }}` | `CRONOVA_LOGICAL_DATE` | The run's logical date, `YYYY-MM-DD` |
| `{{ logical_datetime }}` | `CRONOVA_LOGICAL_DATETIME` | The logical date-time, RFC 3339 |
| `{{ run_id }}` | `CRONOVA_RUN_ID` | Unique id of this run |
| `{{ dag_id }}` | `CRONOVA_DAG_ID` | The DAG id |
| `{{ task_id }}` | `CRONOVA_TASK_ID` | This task's id |
| `{{ try_number }}` | `CRONOVA_TRY_NUMBER` | Attempt number (increments on retry) |

!!! tip
    The **logical date** is the period a run *represents*, not wall-clock "now". That distinction is what makes `catchup` backfills meaningful: a backfilled run for June 3rd sees `{{ logical_date }}` as `2026-06-03` even if it executes today. See [Scheduling](scheduling.md) for how logical dates are assigned.

## Use a template in a command

Update `dags/daily_etl.yaml` so the pipeline knows which day it is processing:

```yaml
dag_id: daily_etl
schedule: "0 2 * * *"
start_date: 2026-06-01
catchup: false
tasks:
  - id: extract
    type: shell
    command: echo "extracting data for {{ logical_date }}"
  - id: transform
    type: shell
    command: echo "transforming for $CRONOVA_LOGICAL_DATE (run $CRONOVA_RUN_ID, attempt $CRONOVA_TRY_NUMBER)"
    deps: [extract]
```

The `extract` task uses the **template form**: the workflow scheduler substitutes `{{ logical_date }}` into the command string when the task is dispatched, so the shell receives something like `echo "extracting data for 2026-07-07"`. Spaces inside the braces are optional — `{{logical_date}}` works too.

Trigger a run and watch it:

```bash
./cronova trigger daily_etl
./cronova runs daily_etl
```

**Check it:** `cronova runs` shows the new run with `extract` and then `transform` reaching `success`. Now open the console at **http://localhost:8090**, click **daily_etl** → the latest run → the **extract** task. Its log reads:

```
extracting data for 2026-07-07
```

with today's date — a manual run's logical date is the moment you triggered it (in UTC).

## Or read the environment variable

The `transform` task above uses the **env var form** instead: `$CRONOVA_LOGICAL_DATE` is plain shell syntax, expanded by the shell at run time from the injected environment. Its log shows all three values:

```
transforming for 2026-07-07 (run daily_etl__manual_..., attempt 1)
```

Both forms carry the same values, so pick whichever fits:

- **Template** (`{{ logical_date }}`) — when the value belongs in the command line itself: `python extract.py --date {{ logical_date }}`.
- **Env var** (`CRONOVA_LOGICAL_DATE`) — when the value is consumed *inside* a script or program. Your Python code just reads `os.environ["CRONOVA_LOGICAL_DATE"]`; nothing in the file needs templating.

`{{ try_number }}` / `CRONOVA_TRY_NUMBER` starts at 1 and increments on each retry — handy for tagging log lines or output files per attempt when you add retries later in [Retries, timeouts & pools](retries-timeouts-pools.md).

## Unknown placeholders are left alone

Substitution only touches placeholders it recognizes. An unknown `{{ ... }}` is left exactly as written, so ordinary shell braces are never mangled:

```yaml
  - id: braces_demo
    type: shell
    command: echo "{{ logical_date }} is replaced, {{ not_a_variable }} is not"
```

**Check it:** trigger the DAG again and open the task's log in the console:

```
2026-07-07 is replaced, {{ not_a_variable }} is not
```

Single braces aren't even candidates — `awk '{ print $1 }'`, `${HOME}`, and brace expansion like `{a,b}` pass through untouched. Only a `{{ name }}` of word characters and dots is considered at all.

## Pills in the console

You don't have to type `{{ }}` by hand. In the console's task editor, every variable renders as a **color-coded pill** inside the command, and a grouped palette (built-in, variables, connections, params) inserts one with a **click or a drag**. Pills are atomic — drag one to move it, hit its **×** to remove it — so a template can never end up half-deleted.

**Check it:** open **http://localhost:8090**, click **daily_etl**, and edit the `extract` task — the `{{ logical_date }}` you wrote in YAML shows up as a pill, and the palette next to the editor offers the other five.

!!! note
    Only the built-in run variables (and per-run trigger params) become `CRONOVA_*` environment variables. UI-managed variables and connections are resolved server-side and enter a command **only** through explicit references — that's the next chapter.

## What you learned

- Six built-in run variables — `logical_date`, `logical_datetime`, `run_id`, `dag_id`, `task_id`, `try_number` — are available both as `{{ ... }}` templates and as `CRONOVA_*` env vars.
- Templates render into the command at dispatch; env vars are read by your script at run time — same values, pick per use case.
- Unknown placeholders pass through untouched, so shell braces are safe.
- The console's pill editor inserts variables by click or drag — no `{{ }}` typing.

Next: keep secrets and settings out of your YAML with [Variables, connections & params](variables-connections-params.md).
