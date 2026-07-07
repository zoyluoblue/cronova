# Variables, connections & params

Hardcoding hostnames, passwords, and dates into DAG YAML is how a workflow scheduler ends up with secrets in git. This chapter teaches cronova's three UI-managed namespaces — `{{ var.KEY }}`, `{{ conn.ID.FIELD }}`, and `{{ params.KEY }}` — so your DAG files stay clean and your secrets stay out of them.

You already know the built-in run variables like `{{ logical_date }}` from the previous chapter. These three namespaces are different: their values live in cronova's database, not in the YAML, and you manage them from the console.

| Namespace | What it holds | Managed where |
|---|---|---|
| `{{ var.KEY }}` | shared config values | console → **Variables & Connections** |
| `{{ conn.ID.FIELD }}` | credentials for external systems | console → **Variables & Connections** |
| `{{ params.KEY }}` | per-run values you pass at trigger time | `cronova trigger -params` or the console |

## Shared variables: `{{ var.KEY }}`

A **variable** is a named value shared across all DAGs — an API base URL, a bucket name, a webhook. Change it once in the console and every task that references it picks up the new value on its next run.

Open the console at **http://localhost:8090** and go to the **Variables & Connections** page. Add a variable with key `greeting` and value `hello from a shared variable`. Names allow letters, digits, and `_ . -`.

Now reference it from a DAG. Create `dags/use_vars.yaml`:

```yaml
dag_id: use_vars
tasks:
  - id: show
    type: shell
    command: echo "{{ var.greeting }}"
```

Trigger it and check the result:

```bash
./cronova trigger use_vars
./cronova runs use_vars
```

In the console, click into the run and open the `show` task's log — it prints `hello from a shared variable`. The `{{ var.greeting }}` placeholder was substituted at dispatch, fetched from the store at that moment.

Edit the variable's value in the console and trigger again: the new run prints the new value. No YAML change, no reload.

## Connections: `{{ conn.ID.FIELD }}`

A **connection** bundles the credentials for one external system — a database, an API, a warehouse — under a single id. On the same **Variables & Connections** page, create a connection with id `api` and fill in its fields.

Each connection has a fixed set of fields you can reference:

| Field | Reference |
|---|---|
| host | `{{ conn.api.host }}` |
| port | `{{ conn.api.port }}` |
| login | `{{ conn.api.login }}` (alias: `{{ conn.api.user }}`) |
| password | `{{ conn.api.password }}` |
| type | `{{ conn.api.type }}` |
| any extra JSON field | `{{ conn.api.extra.KEY }}` |

Use them anywhere a template works — a shell command, or an `http` task's URL, headers, and body:

```yaml
dag_id: call_api
tasks:
  - id: fetch
    type: shell
    command: 'curl -s -u {{ conn.api.login }}:{{ conn.api.password }} "https://{{ conn.api.host }}/status"'
  - id: ingest
    type: http
    deps: [fetch]
    http:
      method: POST
      url: "https://{{ conn.api.host }}/ingest"
      headers: { Authorization: "Bearer {{ var.TOKEN }}" }
      body: '{"date":"{{ logical_date }}"}'
      expected_status: [200, 201]
```

!!! note

    For `sql` tasks you usually don't template fields at all. Set the task-level
    `conn: warehouse` field instead — the connection's `type` picks the driver
    (postgres / mysql / sqlite) and cronova builds the DSN for you. See the
    [DAG Reference](../DAG_REFERENCE.md#task-types).

## Per-run params: `{{ params.KEY }}`

Variables and connections are shared state. **Params** are the opposite: values you pass for *one specific run* when you trigger it manually — a date to reprocess, a customer id, a dry-run flag.

Create `dags/daily_report.yaml` (no `schedule`, so it's manual-only):

```yaml
dag_id: daily_report
tasks:
  - id: build
    type: shell
    command: echo "building report for {{ params.day }} (env says $CRONOVA_PARAM_DAY)"
```

Trigger it with params as a JSON object:

```bash
./cronova trigger daily_report -params '{"day":"2026-01-01"}'
```

Check the `build` task's log in the console: both forms print `2026-01-01`. Every param is available two ways — as the `{{ params.KEY }}` template *and* as a `CRONOVA_PARAM_<KEY>` environment variable (key uppercased), so scripts that only read the environment work too.

From the console, the **⋯** button next to Trigger opens a **Trigger with params** dialog — a key/value form that does the same thing without JSON.

!!! tip

    In the console's task editor you never type the `{{ }}` braces. Each
    reference renders as a color-coded **pill**, and a grouped palette
    (built-in · variables · connections · params) inserts them by click or drag.

## How secrets stay contained

cronova resolves `var.*` and `conn.*` lazily, server-side, at dispatch — and **only when a task explicitly references them**. They are never blanket-injected into every task's environment, so a connection password can't leak into the env (or the logged `env` output) of an unrelated task. Only the built-in run variables and trigger params become `CRONOVA_*` environment variables.

!!! warning

    A template is substituted into the command before it runs, so a task that
    does `echo {{ conn.api.password }}` will print the secret into its own log.
    Reference secrets where they're consumed (an auth flag, a header) — don't
    echo them.

## What you learned

- `{{ var.KEY }}` and `{{ conn.ID.FIELD }}` pull shared config and credentials from the console's **Variables & Connections** page — out of your DAG YAML.
- Connection fields are `host`, `port`, `login`/`user`, `password`, `type`, and `extra.KEY`; `sql` tasks take a task-level `conn:` id instead.
- `cronova trigger <dag_id> -params '{"day":"…"}'` passes per-run values, readable as `{{ params.KEY }}` or `CRONOVA_PARAM_<KEY>`.
- Variables and connections resolve only when referenced — secrets are never injected into every task's environment.

**Next:** wire a real codebase into a task — upload it once and run it as a [project](projects.md).
