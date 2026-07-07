# cronova FAQ — Frequently Asked Questions

Answers to the most common questions about cronova, the single-binary, self-hosted **workflow scheduler** and open-source Airflow / Azkaban alternative — what it is, how it installs, where it stores data, and how to run it in production.

This page expands on the short FAQ in the [README](../README.md). For task-by-task guides see [Getting Started](GETTING_STARTED.md), [DAG Reference](DAG_REFERENCE.md), [CLI Reference](CLI.md), [AI Agents (MCP)](AGENTS.md), [Deployment](DEPLOY.md), and [Architecture](ARCHITECTURE.md).

## What is cronova?

cronova is an open-source, self-hosted **workflow scheduler** (a.k.a. job scheduler / DAG orchestrator) written in Go. It schedules **DAGs** — directed acyclic graphs of tasks — on cron or interval triggers, runs each task as an OS subprocess using the host's own interpreters, and ships a web console, a REST API, a CLI, and an MCP endpoint for AI agents — all in a single static binary with an embedded SQLite database.

## Is cronova an Apache Airflow alternative?

Yes. cronova is a lightweight alternative to [Apache Airflow](https://airflow.apache.org/) and Azkaban for teams who want DAG scheduling — dependencies, retries, catchup / backfill, resource pools, a web UI, and a REST API — **without** running a Python stack, a separate database, and a message broker. It is one binary with an embedded database. For very large, plugin-heavy data platforms, Airflow remains the richer ecosystem. See [cronova vs Airflow](COMPARISON.md) for a feature-by-feature breakdown.

## Does cronova need a separate database, a JVM, or Python?

No. The scheduler and web console are a **single Go binary** with an **embedded SQLite** database (pure-Go `modernc.org/sqlite`, CGO-free), so there is no external Postgres/MySQL, no Redis or Celery broker, no JVM, and no Python runtime to install. Python, Java, `psql`, and other interpreters are only needed on the host if *your tasks* invoke them.

## What languages can tasks be written in?

Any language on the host. Tasks have a `type` of `shell`, `python`, `sql`, `jar`, or `http`, and a `shell` task can invoke anything on the machine — Node, Go, Rust binaries, CLIs, and more. The scheduler (Go) is fully decoupled from the task language: each task runs as an OS subprocess with the host's own interpreters. The `sql` and `http` task types run in-process (drivers/HTTP client are built into the binary) and need nothing extra installed. See the [DAG Reference](DAG_REFERENCE.md) for every task type.

## How is cronova different from cron?

Plain `cron` runs isolated commands on a clock. cronova runs **DAGs**: tasks with dependencies, retries, timeouts, catchup / backfill, concurrency pools, cross-DAG triggers, a web console with live log tailing, and a REST API — the orchestration you normally end up hand-rolling around a `crontab`. cronova still speaks cron syntax (`schedule: "0 2 * * *"`) and also supports `@every 30s` intervals and manual-only DAGs.

## Can AI agents control cronova (MCP)?

Yes. cronova ships a built-in **Model Context Protocol (MCP) server** (`cronova mcp`) that exposes ~30 tools (`list_dags`, `create_dag`, `validate_dag`, `trigger_dag`, `get_task_log`, `retry_task`, …), plus a remote JSON CLI. Agents drive cronova through the **same token-authenticated, role-gated API** humans use — an agent's reach is exactly its token's role (`admin` = full CRUD + operate, `viewer` = read-only), and `cronova mcp -read-only` exposes only the read tools. Tokens are minted locally with `cronova tokens create`, never via the API. Full setup: [AI Agents (MCP)](AGENTS.md).

## Which platforms are supported, and how do I install cronova?

cronova runs on **Linux and macOS**, on both **amd64 and arm64**. The fastest path is the one-line installer, which downloads the matching prebuilt release, verifies its SHA256, installs the native service (systemd on Linux, launchd on macOS), and runs an interactive setup wizard:

```bash
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

Prefer to build from source? With Go 1.26+:

```bash
go build -o cronova ./cmd/cronova
./cronova serve                 # console at http://localhost:8090
```

Prebuilt binaries are on the [Releases](https://github.com/zoyluoblue/cronova/releases) page. Full deployment guide: [Deployment](DEPLOY.md).

## What port does the console use?

The web console and REST API default to port **8090** (config default `:8090`, i.e. all interfaces). Open `http://localhost:8090` after `cronova serve`. Change it with the `-http` flag, the `CRONOVA_HTTP` env var, or the `http:` key in `cronova.yaml` — e.g. `127.0.0.1:8090` to bind local-only behind a reverse proxy.

## How do I upgrade cronova?

Run `cronova update`. It downloads the latest prebuilt release for your OS/arch from GitHub, verifies it against `SHA256SUMS` (the same trust model as the installer), atomically swaps the binary, refreshes the service definition, and restarts the service:

```bash
cronova update                               # latest release, then restart
cronova update v0.2.1                         # pin or downgrade to a specific tag
cronova update -proxy http://127.0.0.1:7890   # download through a proxy
```

An unpinned update that is already current is a no-op. A pinned version always applies, so re-install and downgrade both work. `update` needs root and **auto-elevates via `sudo`** — set `CRONOVA_NO_SUDO=1` to manage privileges yourself. It does **not** touch your config, database, or DAGs. Behind a restricted network, `-proxy` also honors `CRONOVA_UPDATE_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY`. See [Deployment → Updating](DEPLOY.md#updating).

## Is the update safe if it fails halfway?

Yes. `update` backs up the old binary and service definition before swapping, then restarts and **confirms the service actually stays running** (not just that it loaded). If the restart fails, it automatically rolls the binaries back and brings the previous version back up — the box is never left on a half-applied update. A missing `SHA256SUMS` is a warning; a checksum mismatch is fatal, and downgrade-to-cleartext redirects are refused.

## Is cronova crash-safe / production-ready?

cronova is designed for reliable operation. Run tasks in the decoupled **gRPC executor** (point `serve` at it with `-executor` / `CRONOVA_EXECUTOR`) and the scheduler can restart or upgrade **without killing running jobs** — on recovery it re-attaches to in-flight tasks with no double execution. With the default in-process executor, a restart ends running tasks. The service runs under systemd/launchd with a mild sandbox, atomic self-updates with rollback, and an audit trail. See [Deployment](DEPLOY.md) and [Architecture](ARCHITECTURE.md) for the execution model.

## Where does cronova store its data?

State lives in an **embedded SQLite database** plus on-disk DAG YAML, task logs, and uploaded projects. For a `cronova serve` run from a working directory the defaults are relative: `data/cronova.db` (DB), `dags/` (DAGs), and `logs/` (task logs). Uploaded projects default to `~/.cronova/projects`. When installed as a native service the paths are absolute:

| Purpose | Linux (systemd) | macOS (launchd) |
|---|---|---|
| SQLite DB | `/var/lib/cronova/cronova.db` | `/usr/local/var/cronova/cronova.db` |
| DAG YAML | `/var/lib/cronova/dags/` | `/usr/local/var/cronova/dags/` |
| task logs | `/var/log/cronova/` | `/usr/local/var/log/cronova/` |
| config | `/etc/cronova/cronova.yaml` | `/usr/local/etc/cronova/cronova.yaml` |
| uploaded projects | `~cronova/.cronova/projects/` | `~/.cronova/projects/` (sudo user) |

Override any of these with the `-db` / `-dags` / `-logs` / `-projects` flags, the `CRONOVA_DB` / `CRONOVA_DAGS` / `CRONOVA_LOGS` / `CRONOVA_PROJECTS` env vars, or `cronova.yaml`. Full layout table: [Deployment → Platform layout](DEPLOY.md#platform-layout).

## How do I run cronova behind a reverse proxy?

Bind cronova to localhost and terminate TLS at your proxy (nginx, Caddy, Traefik, …). The one-click wizard offers a **"this machine only (127.0.0.1)"** bind option for exactly this, or set `CRONOVA_HTTP=127.0.0.1:8090` (or `-http 127.0.0.1:8090`). When serving over HTTPS, set `CRONOVA_SECURE_COOKIE=true` (default `false`) so the session cookie is marked `Secure`. The console, REST API, and live-log SSE stream all share the one HTTP listener, so a single upstream is enough. See [Deployment](DEPLOY.md).

## Do I need Docker or Kubernetes?

No. cronova is a subprocess scheduler that runs tasks with the **host's own interpreters**, so it deploys as a single static binary under systemd (Linux) or launchd (macOS) — no container image to build, no runtime to bundle. Containerizing a polyglot scheduler would force you to bake every task runtime into the image; native + systemd sidesteps that. If you do want the scheduler containerized, run the standalone `cronova-executor` on the host and point the scheduler at it over gRPC. See [Deployment → Why not Docker?](DEPLOY.md).

## How do I uninstall cronova?

Run `cronova uninstall`. It stops and removes the native service and the binary but **keeps your data** (config, DB, DAGs, logs), so a plain uninstall is reversible by re-installing. Add `--purge` to also delete the data:

```bash
cronova uninstall            # remove service + binary, KEEP data
cronova uninstall --purge    # also delete config, DB, DAGs, logs
cronova uninstall -yes       # skip the confirmation prompt (for scripts)
```

Like other mutating commands, `uninstall` needs root and auto-elevates via `sudo`. See [Deployment → Uninstalling](DEPLOY.md#uninstalling).

## What license is cronova released under?

cronova is released under the **[MIT License](../LICENSE)** — a permissive license that allows commercial and private use, modification, and redistribution.

## See also

- [README](../README.md) — project overview and quick start
- [Getting Started](GETTING_STARTED.md) — install, first DAG, projects, template variables
- [DAG Reference](DAG_REFERENCE.md) — every DAG/task field, task types, triggers, pools
- [CLI Reference](CLI.md) — every `cronova` command and flag
- [AI Agents (MCP)](AGENTS.md) — MCP server, remote CLI, tokens, security
- [Deployment](DEPLOY.md) — systemd/launchd, updates, crash-recoverable executor
- [Architecture](ARCHITECTURE.md) — design rationale, execution model, diagrams
- [cronova vs Airflow](COMPARISON.md) — when to choose cronova, feature-by-feature
