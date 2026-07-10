# Cross-DAG triggers & notifications

So far every dependency lived *inside* one DAG. In this final chapter you'll chain whole DAGs together with `trigger_after`, see the result in the console's **DAG Graph**, and wire a `notify` webhook so a failed run pings you instead of waiting to be discovered.

## Task deps vs. DAG deps

`deps` wires tasks within a single run of a single DAG. `trigger_after` works one level up: it runs an **entire downstream DAG** after another DAG succeeds. That's the natural shape when one pipeline ends where another begins — an ingest DAG owned by one team, a reporting DAG owned by another — without merging them into one giant workflow.

cronova ships both halves of this pattern as runnable examples in the repo's [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags) directory: `upstream_ingest.yaml` and `downstream_report.yaml`.

## The upstream DAG

The upstream is a completely ordinary DAG — nothing in it knows a downstream exists:

```yaml
dag_id: upstream_ingest
# manual/scheduled upstream; downstream_report runs after this succeeds
start_date: 2026-06-01
max_active_runs: 1
tasks:
  - id: ingest
    type: shell
    command: "echo ingesting for $CRONOVA_LOGICAL_DATE && sleep 1"
```

It has no `schedule`, so it runs only when triggered — convenient for this walkthrough, but a cron schedule works exactly the same way.

## The downstream DAG: `trigger_after`

The downstream declares the dependency on *its* side:

```yaml
dag_id: downstream_report
start_date: 2026-06-01
max_active_runs: 1
default_retries: 1
# runs automatically once upstream_ingest succeeds for the same logical_date
trigger_after:
  - dag_id: upstream_ingest
tasks:
  - id: build_report
    type: shell
    command: "echo building report from $CRONOVA_LOGICAL_DATE data"
    pool: reports        # configure size with: cronova pools set reports <n>
    retries: 2
    timeout: 600
```

Two things to notice:

- **No `schedule`.** A DAG with an empty schedule runs only on manual trigger or `trigger_after` — the upstream *is* its schedule.
- **`trigger_after` names the upstream.** Whenever `upstream_ingest` finishes a run in `success`, the scheduler creates a run of `downstream_report` for the **same logical date**.

Because the logical date carries over, a backfilled upstream run triggers a report for *that* period — not for wall-clock "now". Catchup and cross-DAG triggers compose for free.

!!! tip
    `trigger_after` takes a list, so a downstream can fan in from several upstreams. It fires only when **every** listed upstream has a successful run for that logical date, and it still respects the downstream's own `max_active_runs`.

The success signal is durable: cronova commits it with the upstream run's final
state. If the global queued-run limit is full, the signal stays pending and is
retried on later scheduler ticks. Once admitted, the downstream waits in
`queued` until its own `max_active_runs` slot is available.

## Trigger the chain

With `cronova serve` running, fire the upstream:

```bash
./cronova trigger upstream_ingest
```

Give it a few seconds (the ingest task sleeps for one, and the scheduler ticks every 2s), then check both DAGs:

```bash
./cronova runs upstream_ingest
./cronova runs downstream_report
```

The upstream shows a normal manual run:

```
RUN_ID                             LOGICAL_DATE          STATE    TRIGGER  TASKS
upstream_ingest__20260707T091502Z  2026-07-07T09:15:02Z  success  manual   ingest=success
```

And the downstream shows a run **you never triggered**:

```
RUN_ID                               LOGICAL_DATE          STATE    TRIGGER     TASKS
downstream_report__20260707T091502Z  2026-07-07T09:15:02Z  success  dependency  build_report=success
```

The `TRIGGER` column reads `dependency`, and `LOGICAL_DATE` matches the upstream run exactly — that's the cross-DAG trigger at work.

## See it in the DAG Graph

Open the console at **http://localhost:8090** and click **Graph** in the navigation. The **DAG Graph** view draws the trigger dependencies between DAGs — you'll see an edge from `upstream_ingest` to `downstream_report`. As your workflow scheduler grows to dozens of DAGs, this graph is how you answer "what runs after what?" at a glance.

## Webhook notifications with `notify`

A pipeline that fails silently is worse than no pipeline. `notify` is a DAG-level field: a URL that receives a JSON `POST` when a run finishes in a state you list. Add it to any DAG:

```yaml
dag_id: downstream_report
# … tasks as above …
notify:
  url: https://hooks.slack.com/services/T000/B000/XXXX
  on: [failure]
```

`on` accepts `failure` and/or `success` — `failure` also covers cancelled and timed-out runs, so anything non-green alerts. The URL must be `http(s)`.

The payload looks like this:

```json
{
  "text": "cronova · downstream_report · run downstream_report__20260707T091502Z finished: failed (tasks: [build_report])",
  "dag_id": "downstream_report",
  "run_id": "downstream_report__20260707T091502Z",
  "state": "failed",
  "logical_date": "2026-07-07T09:15:02Z",
  "started_at": "2026-07-07T09:15:04Z",
  "finished_at": "2026-07-07T09:15:07Z",
  "duration_ms": 3000,
  "failed_tasks": ["build_report"]
}
```

The `text` field is a ready-made human summary, so a Slack, Feishu, or Discord **incoming webhook** URL renders it directly with zero glue code; the structured fields serve your own endpoints.

To see it fire, temporarily change `build_report`'s command to `exit 1`, trigger `upstream_ingest` again, and watch the `cronova serve` log: you'll see a `notify sent` line with the run id (or `notify non-2xx` / `notify post` if delivery failed). Delivery is asynchronous and best-effort — it never blocks the scheduling loop.

If you set `sla` or `dagrun_timeout` on a DAG, breaches report through the same webhook — configuring the threshold is itself the opt-in, independent of `on`.

!!! warning
    Outbound webhooks are SSRF-hardened: cronova refuses URLs that resolve to private or internal addresses (localhost, RFC 1918 ranges, link-local, cloud metadata) and never follows redirects. Test against a public endpoint — a real Slack/Feishu webhook or a hosted request-inspection service — not a receiver on `http://localhost`.

## What you learned

- `trigger_after` chains whole DAGs: the downstream runs automatically once every listed upstream has a **successful run for the same logical date**, showing `TRIGGER=dependency` in `cronova runs`.
- A DAG with no `schedule` runs only on manual trigger or `trigger_after`, and the console's **Graph** view visualizes all cross-DAG edges.
- `notify` POSTs a JSON payload — with a Slack-ready `text` summary — on `failure` and/or `success`, delivered asynchronously with SSRF protection built in.

## Where to go next

That's the whole tutorial — you've gone from a single `echo` task to a scheduled, dependency-aware, retry-hardened, cross-DAG pipeline with alerting, all on a compact native install with embedded SQLite. From here:

- **[Deployment](../DEPLOY.md)** — install cronova as a systemd/launchd service, switch to the crash-recoverable gRPC executor so running tasks survive a scheduler restart or upgrade, and keep it updated with `cronova update`.
- **[AI Agents (MCP)](../AGENTS.md)** — let AI agents list, create, validate, and trigger DAGs through the built-in MCP server and the remote JSON CLI.
- **[DAG & Task Reference](../DAG_REFERENCE.md)** — the exhaustive schema: every DAG and task field, all five task types, trigger rules, and pools.
- **[CLI Reference](../CLI.md)** — every command and flag, from `serve` to `tokens create`.
