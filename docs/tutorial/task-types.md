# Task types: shell, python, sql, jar, http

Every task in a cronova **DAG** has a `type` that tells the workflow scheduler *how* to execute it. This chapter walks through all five — `shell`, `python`, `sql`, `jar`, and `http` — with a minimal runnable example of each, and shows which ones need tools installed on the host and which are self-contained in the binary.

## `shell` — run any command

A `shell` task runs its `command` as an OS subprocess via `sh -c`, so pipes, redirection, `$(...)` substitution, and environment variables all work exactly as they do in your terminal.

Create `dags/type_shell.yaml` (no `schedule`, so it only runs when you trigger it):

```yaml
dag_id: type_shell
tasks:
  - id: hello
    type: shell
    command: echo "hello from $(uname -s) at $CRONOVA_LOGICAL_DATETIME"
```

Trigger it and check the result:

```bash
cronova trigger type_shell
cronova runs type_shell
```

The `hello` task goes to **success**. Open the run in the console at [http://localhost:8090](http://localhost:8090) and click the task — the log shows something like `hello from Darwin at 2026-07-07T09:00:00Z`.

!!! note
    `shell` is the **default** type — every task you wrote in the earlier chapters was a shell task. You can omit `type: shell` entirely.

A shell task can invoke anything installed on the host: a Python script, a Node CLI, `psql`, a compiled binary. It is also the only type that accepts the `project` field from the [previous chapter](projects.md).

## `python` — inline Python code

A `python` task puts Python **code** in `command` and runs it with `python3` (falling back to `python`) from the service `PATH`. Use a YAML block scalar (`|`) for multiple lines. The code is passed to the interpreter as an argument — not through a shell — so you never have to escape quotes.

Create `dags/type_python.yaml`:

```yaml
dag_id: type_python
tasks:
  - id: crunch
    type: python
    command: |
      import os, platform
      print("python", platform.python_version())
      print("processing", os.environ["CRONOVA_LOGICAL_DATE"])
```

The `CRONOVA_*` run variables are in the environment, just like in a shell task.

Trigger and check:

```bash
cronova trigger type_python
cronova runs type_python
```

The task log shows the interpreter version and the logical date. The task's result is the interpreter's exit code, so an uncaught exception fails the task — and triggers retries, if you configure them. If there is no interpreter on the service `PATH`, the log says `python: no python3/python interpreter on PATH` and the task fails.

## `sql` — query a database, no client tools

A `sql` task holds the query in `command` and names a [connection](variables-connections-params.md) with `conn`. cronova opens the database itself with a native driver compiled into the binary — the connection's *type* selects PostgreSQL, MySQL/MariaDB, or SQLite — so there is no `psql` or `mysql` client to install.

```yaml
dag_id: type_sql
tasks:
  - id: count_events
    type: sql
    conn: warehouse
    command: "SELECT count(*) FROM events WHERE day = '{{ logical_date }}'"
```

Here `warehouse` is the id of a connection you created in the console, and the query is templated per run, like any other `command`.

The task log shows the result: a row-returning statement (`SELECT`, `WITH`, `SHOW`, …) logs tab-separated columns and rows (the first 100) followed by a row count; any other statement logs `(N rows affected)`.

!!! tip
    You can try `sql` tasks with zero infrastructure: create a connection with type `sqlite` and set its **host** to a file path (for SQLite, host holds the database file). Point `conn` at it with `command: "SELECT 1 AS ok"` and trigger — the log reads:

    ```
    ok
    1
    (1 rows)
    ```

## `jar` — run a Java program

A `jar` task runs a `java -jar …` command line. It executes with the same shell semantics as a `shell` task — flags, quoting, env vars, and `{{ }}` templates all behave identically — the type documents that this task is a Java job. It needs a JRE/JDK on the service `PATH`.

```yaml
dag_id: type_jar
tasks:
  - id: report
    type: jar
    command: "java -jar /opt/jobs/report.jar --date {{ logical_date }}"
```

Check it the same way: trigger, then read the program's stdout in the task log. A non-zero exit from the JVM fails the task. If the task fails instantly with a "command not found"-style log line, `java` isn't on the `PATH` the *service* sees — verify with `java -version` in that environment.

## `http` — call an API, no curl

An `http` task doesn't use `command` at all. Instead you describe the request under the task's `http:` key, and cronova performs it with an in-process HTTP client — nothing to install, and it follows redirects.

| Field | Default | Meaning |
|---|---|---|
| `method` | `GET` | HTTP method |
| `url` | — (required) | Request URL; supports `{{ }}` templates |
| `headers` | — | Header map; values support templates |
| `body` | — | Request body; supports templates |
| `expected_status` | any 2xx | Status codes counted as success, e.g. `[200, 201]` |

The smallest possible example — create `dags/type_http.yaml`:

```yaml
dag_id: type_http
tasks:
  - id: ping
    type: http
    http:
      url: https://example.com
```

Trigger it, then open the task log in the console. You'll see a request/response transcript:

```
> GET https://example.com
< 200 OK (142ms)
<!doctype html>
…
```

If the status isn't accepted, the log ends with `http: unexpected status 503 (want 2xx)` and the task fails — which, again, is what drives retries.

A realistic call combines templates in the URL, headers, and body:

```yaml
  - id: ingest
    type: http
    http:
      method: POST
      url: "https://{{ conn.api.host }}/ingest"
      headers:
        Authorization: "Bearer {{ var.TOKEN }}"
      body: '{"date": "{{ logical_date }}"}'
      expected_status: [200, 201]
```

The host comes from a connection, the token from a managed variable — so no secret ever sits in the YAML file.

## Self-contained vs. host tools

The five types split cleanly into two groups:

| Type | Runs as | `command` holds | Needs on the host |
|---|---|---|---|
| `shell` | OS subprocess (`sh -c`) | any shell command | the tools the command invokes |
| `python` | OS subprocess (`python3`) | Python code | `python3` on the service `PATH` |
| `sql` | in-process (native driver) | the SQL query; `conn` selects the connection | nothing extra |
| `jar` | OS subprocess (`java`) | a `java -jar …` command | a JRE/JDK on the `PATH` |
| `http` | in-process HTTP client | — (use the `http:` spec) | nothing extra |

`sql` and `http` are built into the cronova binary and work on a bare host. `shell`, `python`, and `jar` — and anything a shell task invokes — need that tool installed where the scheduler runs.

!!! warning
    When cronova runs as a systemd or launchd service, tasks inherit the **service's** `PATH`, which is usually much shorter than your interactive shell's. A command that works in your terminal can still fail as `command not found` under the service — see [Deployment](../DEPLOY.md) for the fix.

The full field-by-field schema for every type is in the [DAG Reference](../DAG_REFERENCE.md).

## What you learned

- Every task has a `type`; `shell` is the default and runs `command` through `sh -c`.
- `python` runs inline code with `python3`, and `jar` runs a `java -jar` command with shell semantics — both need their runtime on the service `PATH`.
- `sql` (a `conn` id plus a query) and `http` (an `http:` spec with `method`, `url`, `headers`, `body`, `expected_status`) are self-contained in the binary, with templates available in queries, URLs, headers, and bodies.

Next: make tasks resilient with [Retries, timeouts & pools](retries-timeouts-pools.md).
