# Tutorial

This tutorial teaches you cronova — the lightweight, self-hosted **workflow scheduler** and open-source Airflow alternative — step by step, from installing the release to running a production-shaped pipeline with retries, pools, and cross-DAG dependencies.

Each chapter builds on the previous one, but is also written to stand on its own. If you want a specific topic, jump straight to it.

## What you'll build

You'll grow one small **ETL-style pipeline** chapter by chapter. It starts as a single `echo` task in a YAML file, then gains a real **cron** schedule with backfill, an extract → transform → load dependency chain, date-templated commands, secrets from managed connections, your own uploaded Python project, retries and a resource pool — and finally a downstream reporting **DAG** that runs whenever the pipeline succeeds.

Everything runs locally: one `cronova` binary, an embedded SQLite database, and the web console at **http://localhost:8090**. No external database, no message broker, no containers.

## What you need

- A **Linux or macOS** machine — a laptop is fine. Prebuilt binaries (latest release: **v0.2.1**) cover amd64 and arm64.
- A terminal.
- **Go 1.26.5+**, *only* if you choose to build from source. The prebuilt release and the one-line installer need no toolchain at all.

!!! tip
    The binary is CGO-free (pure-Go SQLite), so there is nothing to compile or link against — download, `chmod +x`, run.

## Chapters

1. **[Install cronova](install.md)** — get the binary (prebuilt release, one-line installer, or `go build`), run `cronova serve`, and open the console.
2. **[Your first DAG](first-dag.md)** — write a DAG as a YAML file in `./dags`, trigger it, and watch the run in the console and CLI.
3. **[Scheduling](scheduling.md)** — cron expressions and `@every` intervals, `start_date`, `catchup` backfill, and what the *logical date* means.
4. **[Task dependencies](dependencies.md)** — wire tasks together with `deps` and control when they fire with trigger rules like `all_success` and `one_failed`.
5. **[Template variables](template-variables.md)** — inject `{{ logical_date }}`, `{{ run_id }}`, and friends into commands, or read them as `CRONOVA_*` environment variables.
6. **[Variables, connections & params](variables-connections-params.md)** — keep secrets and settings out of YAML with `{{ var.KEY }}`, `{{ conn.ID.field }}`, and per-run `{{ params.KEY }}`.
7. **[Projects: run your own code](projects.md)** — upload a script or a whole codebase and run it from a fresh, isolated working directory per attempt.
8. **[Task types](task-types.md)** — beyond `shell`: `python`, `sql`, `jar`, and `http` tasks, and when to use each.
9. **[Retries, timeouts & pools](retries-timeouts-pools.md)** — make the pipeline resilient with `retries`, `retry_delay`, `timeout`, SLAs, and global concurrency pools.
10. **[Cross-DAG dependencies](cross-dag.md)** — chain whole DAGs with `trigger_after` and get webhook notifications on success or failure.

!!! note
    The tutorial covers the fields and commands you'll use daily. The exhaustive schema lives in the [DAG Reference](../DAG_REFERENCE.md), and every command and flag in the [CLI Reference](../CLI.md). Runnable example DAGs are in the repo's [`dags/`](https://github.com/zoyluoblue/cronova/tree/main/dags) directory.

## How to read it

Every chapter follows the same rhythm: a short explanation, a small runnable snippet (a YAML fragment or a couple of shell commands), and a **check-it** moment — the exact CLI output or console change that proves it worked. Type along; each step takes a minute or two.

## What you learned

- The tutorial evolves one ETL-style pipeline from a single task into a scheduled, dependency-aware, retry-hardened workflow.
- You need only a Linux/macOS machine — Go 1.26.5+ is required only for source builds.
- Ten chapters take you from install to cross-DAG orchestration, with reference docs for everything deeper.

Next up: get the binary running in [Install cronova](install.md).
