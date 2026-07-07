# cronova Documentation

Guides and reference for **cronova** — a lightweight, self-hosted **workflow scheduler** in a single Go binary, and an open-source [Apache Airflow](https://airflow.apache.org/) / Azkaban alternative. Start here to install cronova, define DAGs, run polyglot tasks, deploy to production, and let AI agents drive it over MCP.

New to cronova? Read the [project overview](../README.md) first, then [Getting Started](GETTING_STARTED.md).

## Get started

- **[Getting Started](GETTING_STARTED.md)** — install, run `cronova serve`, write your first DAG, template variables, and uploading your own scripts/projects.
- **[Console Guide](console/index.md)** — every page of the web UI: dashboard, DAG editor, visual task editor, runs & live logs, pools, variables, audit, API tokens.
- **[Deployment](DEPLOY.md)** — one-command install, systemd/launchd services, `cronova update`, and the crash-recoverable gRPC executor.

## Reference

- **[DAG & Task Reference](DAG_REFERENCE.md)** — every DAG and task field, the `shell` / `python` / `sql` / `jar` / `http` task types, triggers, trigger rules, and resource pools.
- **[CLI Reference](CLI.md)** — every `cronova` command, subcommand, and flag.
- **HTTP API** — a machine-readable OpenAPI spec is served at `GET /openapi.json`; human-readable Redoc at `/docs` on a running console.

## Operate & integrate

- **[AI Agents (MCP)](AGENTS.md)** — the built-in Model Context Protocol server, the remote JSON CLI, API tokens, and role-based security.

## Concepts & background

- **[Architecture](ARCHITECTURE.md)** — the execution model, scheduler loop, crash recovery, and design rationale.
- **[Design Notes](DESIGN.md)** — deeper design decisions and trade-offs.
- **[cronova vs. Airflow, Azkaban, Dagster, Prefect & cron](COMPARISON.md)** — a feature-by-feature comparison and when to choose cronova.
- **[FAQ](FAQ.md)** — frequently asked questions, answered.

---

<sub>cronova — self-hosted <b>workflow scheduler</b> · <b>Airflow alternative</b> · DAG orchestration · single Go binary · MCP-ready for AI agents. See the <a href="../README.md">README</a> · <a href="../README.zh-CN.md">简体中文</a>.</sub>
