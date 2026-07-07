---
title: cronova — a lightweight, self-hosted workflow scheduler
hide:
  - navigation
  - toc
---

# cronova

**A lightweight, self-hosted workflow scheduler in a single Go binary — an open-source Airflow / Azkaban alternative you can install with one command.**

[![Release](https://img.shields.io/github/v/release/zoyluoblue/cronova?sort=semver&logo=github)](https://github.com/zoyluoblue/cronova/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/zoyluoblue/cronova/blob/main/LICENSE)
[![GitHub stars](https://img.shields.io/github/stars/zoyluoblue/cronova?logo=github&color=1f6feb)](https://github.com/zoyluoblue/cronova/stargazers)

cronova schedules **DAGs** — tasks with dependencies, retries, catchup and pools — and ships as **one static binary** with an **embedded SQLite** database. No JVM, no Python runtime, no external database, no message broker.

```bash
# Install the scheduler + web console + native service on Linux or macOS:
curl -fsSL https://raw.githubusercontent.com/zoyluoblue/cronova/main/deploy/bootstrap.sh | sudo bash
```

[Start the Tutorial](tutorial/index.md){ .md-button .md-button--primary }
[Quick start](GETTING_STARTED.md){ .md-button }

![cronova web console — visual task editor with drag-and-drop template variable pills](img/task-editor.png)

<div class="grid cards" markdown>

-   :material-package-variant-closed:{ .lg .middle } **Single binary, zero dependencies**

    ---

    Pure-Go build, embedded database, one process. `curl | bash` to install,
    `cronova update` to upgrade, `cronova uninstall` to remove.

    [:octicons-arrow-right-24: Quick start](GETTING_STARTED.md)

-   :material-graph:{ .lg .middle } **Airflow-style DAGs, in YAML**

    ---

    Dependencies, cron / `@every` schedules, cross-DAG triggers,
    catchup / backfill, retries & timeouts, resource pools, trigger rules.

    [:octicons-arrow-right-24: DAG & Task Reference](DAG_REFERENCE.md)

-   :material-language-python:{ .lg .middle } **Polyglot tasks + project upload**

    ---

    Tasks run as subprocesses — shell, Python, SQL, a JAR, or HTTP.
    Drag-and-drop a whole project folder and run it in an isolated copy.

    [:octicons-arrow-right-24: Run your own scripts](GETTING_STARTED.md#5-run-your-own-scripts-and-projects)

-   :material-robot:{ .lg .middle } **AI-native (MCP built in)**

    ---

    A built-in Model Context Protocol server and a remote JSON CLI let AI
    agents manage workflows through the same token-authenticated API.

    [:octicons-arrow-right-24: AI Agents (MCP)](AGENTS.md)

</div>

## What is cronova?

cronova is an **open-source, self-hosted workflow scheduler** (job scheduler / task orchestrator / DAG scheduler) written in Go. It schedules DAGs on cron or interval triggers, runs each task as a subprocess with the host's own interpreters, and gives you a web console, a REST API, a CLI, and an MCP endpoint for AI agents. Think of it as a **cron replacement with dependencies, retries, backfill and observability**, or a **lightweight Airflow alternative** that fits in one binary.

## 30 seconds to a running DAG

```bash
go build -o cronova ./cmd/cronova   # or grab a prebuilt release
./cronova serve                     # console at http://localhost:8090
./cronova trigger example_etl       # run a DAG now
./cronova runs example_etl          # watch task states
```

## Learn more

- **[Tutorial](tutorial/index.md)** — the step-by-step path: install → first DAG → scheduling → variables → projects → cross-DAG.
- **[Console guide](console/index.md)** — every page of the web UI: dashboard, DAG editor, visual task editor, runs & live logs, pools, variables, audit, API tokens.
- **[Quick start](GETTING_STARTED.md)** — the single-page fast path.
- **[Deployment](DEPLOY.md)** — systemd / launchd services, updates, the crash-recoverable executor.
- **[Comparison](COMPARISON.md)** — cronova vs. Airflow, Azkaban, Dagster, Prefect & cron.
- **[FAQ](FAQ.md)** — common questions, answered.
- **[GitHub](https://github.com/zoyluoblue/cronova)** — source, releases, issues. ⭐ Stars welcome!
