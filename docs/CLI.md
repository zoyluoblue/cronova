# cronova CLI Reference

Every `cronova` command, subcommand, and flag — the single binary that runs the scheduler, manages the service, operates DAGs, and serves AI agents. Run `cronova <command> -h` for a command's own flags. For first steps see [Getting Started](GETTING_STARTED.md); for the DAG schema see the [DAG Reference](DAG_REFERENCE.md).

```
cronova <command> [args] [flags]
```

Commands fall into three groups: **scheduler & service** (run and manage the installed service), **local operations** (act directly on the SQLite DB), and **agent / remote mode** (act on a running server over its authenticated REST API).

## Scheduler

### `cronova serve`

Run the scheduling loop plus the web console and REST API.

| Flag | Default | Description |
|---|---|---|
| `-http` | `:8090` | HTTP address for the console + API (empty to disable). |
| `-db` | `data/cronova.db` | SQLite metadata database path. |
| `-dags` | `dags` | Directory of DAG YAML definitions. |
| `-logs` | `logs` | Directory for task log files. |
| `-projects` | `~/.cronova/projects` | Directory for uploaded project files. |
| `-executor` | *(in-process)* | gRPC executor target, e.g. `unix:///tmp/cronova-executor.sock`. Empty = in-process executor. |
| `-tick` | `2s` | Scheduling-loop interval. |
| `-auth` | off | Require login for the console/API (overrides config). |
| `-config` | `cronova.yaml` | Path to a YAML config file (optional). |

`CRONOVA_WEB_DIR` (dev only) serves the console assets from disk instead of the embedded copies — edit and reload without a rebuild.

### `cronova-executor` (separate binary)

The standalone, crash-recoverable task executor. The scheduler connects to it with `serve -executor`.

| Flag | Description |
|---|---|
| `-sock <path>` | Unix socket to listen on, e.g. `/tmp/cronova-executor.sock`. |

## Service lifecycle

These wrap the host service manager (systemd on Linux, launchd on macOS). Mutating commands **auto-elevate via `sudo`** (set `CRONOVA_NO_SUDO=1` to manage privileges yourself).

| Command | Description |
|---|---|
| `cronova start` / `stop` / `restart` | Control the installed service. |
| `cronova status` | Show the installed service's status. |
| `cronova init [-yes]` | Interactive first-time setup (port, bind scope, admin, auth). `-yes` accepts defaults/env non-interactively. |
| `cronova update [version] [-proxy URL]` | Download + install the latest release (or a pinned `version`), verify its SHA256, atomically swap the binary, refresh the service definition, and restart — with automatic rollback if the new binary doesn't stay up. |
| `cronova uninstall [--purge] [-yes]` | Remove the service + binary. `--purge` also deletes config, DB, DAGs, and logs. `-yes` skips the prompt. |
| `cronova version` | Print the build version and platform. |
| `cronova healthcheck` | Probe `/readyz` and exit non-zero if unhealthy (handy for monitoring). |

**`update` proxy:** `-proxy http://127.0.0.1:7890` (or `socks5://…`) routes the download through a proxy. It also honors `CRONOVA_UPDATE_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY`, and these survive the `sudo` escalation. See [Deployment → Updating](DEPLOY.md#updating).

## Local operations

Run on the machine that holds the database; they act directly on the SQLite DB (`-db`, default `data/cronova.db`).

| Command | Key flags | Description |
|---|---|---|
| `cronova trigger <dag_id>` | `-params '{"day":"…"}'`, `-db`, `-dags` | Create a manual run, optionally with trigger params. |
| `cronova dags` | `-db`, `-dags` | List registered DAGs. |
| `cronova runs <dag_id>` | `-n N` | Show recent runs and task states. |
| `cronova pools` | | List resource pools and usage. |
| `cronova pools set <name> <slots>` | | Create or resize a pool. |
| `cronova users` | | List console accounts. |
| `cronova users add <name>` | `-role admin\|viewer`, `-password` | Create an account. |
| `cronova users passwd <name>` | `-password` | Change a password. |
| `cronova users delete <name>` | | Remove an account. |

## Agent / remote mode

Drive a running server over its **token-authenticated, role-gated REST API**. Set the target with flags or environment:

| Global flag | Env | Description |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | Server URL, e.g. `http://localhost:8090`. Empty = local DB. |
| `-token <token>` | `CRONOVA_TOKEN` | API token (mint with `cronova tokens create`). |
| `-o table\|json` | `CRONOVA_OUTPUT` | Output format; `json` for scripting/agents. |

```bash
export CRONOVA_SERVER=http://localhost:8090 CRONOVA_TOKEN=cnv_pat_…
cronova dags -o json
```

| Command | Description |
|---|---|
| `cronova api <METHOD> <path> [json-body]` | Raw call to any REST endpoint (e.g. `api POST /api/dags/validate '{…}'`). |
| `cronova get <dag_id>` | Show a DAG definition. |
| `cronova run <run_id>` | Show a run and its task states. |
| `cronova logs <task_instance_id>` | Fetch a task's log. |
| `cronova cancel <run_id>` | Cancel an active run. |
| `cronova retry <run_id> [task_id]` | Retry a run's failed tasks, or one task. |
| `cronova mark <run_id> [task_id] <state>` | Operator override of a run/task state. |
| `cronova pause <dag_id> [-off]` | Pause scheduling, or resume with `-off`. |
| `cronova overview` | Dashboard summary (DAGs / runs / pools). |
| `cronova tokens create <name> [-role admin\|viewer]` | Mint an API token (local, once — not exposed over the API). |
| `cronova tokens list` / `tokens delete <id>` | Manage API tokens. |

### `cronova mcp`

Run a [Model Context Protocol](https://modelcontextprotocol.io/) server over stdio, exposing cronova operations as tools for AI clients (Claude, etc.).

| Flag | Env | Description |
|---|---|---|
| `-server <url>` | `CRONOVA_SERVER` | Server URL (default `http://localhost:8090`). |
| `-token <token>` | `CRONOVA_TOKEN` | API token. |
| `-read-only` | | Expose only the read (GET) tools. |

Full guide, MCP config snippet, and security notes: [AI Agents (MCP)](AGENTS.md).

## See also

- [Getting Started](GETTING_STARTED.md) · [DAG Reference](DAG_REFERENCE.md) · [AI Agents (MCP)](AGENTS.md) · [Deployment](DEPLOY.md) · [FAQ](FAQ.md)
